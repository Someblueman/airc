//go:build linux && arm64

package gateway_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSCTPSubmittedRequestRetryAfterGatewayKillWithAdmissionFenced(t *testing.T) {
	f := newSCTPFixtureShutdown(t, "ipv4", false)
	db, err := sql.Open("sqlite", filepath.Join(f.config.StateDir, "journal.sqlite")+"?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{
		Version: 1, RequestID: "sctp-admission-fenced-retry", Operation: core.Send,
		Target:  &core.Target{Kind: "room", Name: "#gateway-fixture"},
		Message: "SCTP exact-frame retry after admission fence 世界", IssuedAt: &issued, ExpiresAt: &expiry,
	}
	frame := sctpFrame(t, f, request, true)
	wire := sctpRequestBytes(t, frame)
	// The transaction fences admission. A successful send proves transport
	// submission, not that userspace consumed this record before the crash.
	if err := f.client.sendRecord(t, wire, false, time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("submit SCTP request record: %v", err)
	}
	var jobs int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE request_id=?", request.RequestID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("held SQLite transaction observed pre-kill job count=%d err=%v", jobs, err)
	}
	if records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name); len(records) != 0 {
		t.Fatalf("request reached native delivery before journal admission: %+v", records)
	}
	if _, err := f.client.readRecord(t, time.Now().Add(100*time.Millisecond)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("gateway returned a response while journal admission was fenced")
	}
	if err := f.process.command.Process.Kill(); err != nil {
		t.Fatalf("kill gateway with journal admission fenced: %v", err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("SIGKILLed gateway exited cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("killed gateway did not exit and join")
	}
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE request_id=?", request.RequestID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("held SQLite transaction observed post-kill job count=%d err=%v", jobs, err)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false

	restarted := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, restarted)
	f.process = restarted
	f.address = gatewayAddress(t, restarted)
	f.client = dialSCTP(t, f.address)
	if err := f.client.sendRecord(t, wire, false, time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("retry exact SCTP request bytes: %v", err)
	}
	admission := sctpReadResponse(t, f, f.client, frame.CorrelationID, time.Now().Add(5*time.Second))
	if admission.Event != "result" || admission.Result == nil || admission.Result.Result == nil || !admission.Result.Result.Durable || admission.Result.Result.RequestID != request.RequestID {
		t.Fatalf("SCTP retry durable admission=%+v", admission)
	}
	var receipt core.Result
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		lookup := core.Request{Version: 1, RequestID: "sctp-admission-receipt-" + strconv.Itoa(attempt), Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := sctpCall(t, f, f.client, lookup)
		if response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("SCTP retry receipt lookup=%+v", response)
		}
		receipt = *response.Result.Result
		if receipt.State == core.Accepted {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID == "" {
		t.Fatalf("SCTP retry receipt=%+v", receipt)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatalf("SCTP retry native event=%+v", seen)
	}
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != receipt.MessageID || records[0].From != "fixture-author" || records[0].Target != request.Target.Name || records[0].Message != request.Message {
		t.Fatalf("SCTP admission retry duplicated or changed native post: %+v", records)
	}
}
