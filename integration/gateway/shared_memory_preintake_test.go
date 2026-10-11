//go:build (darwin || linux) && arm64

package gateway_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSharedMemoryProducerRetryAfterLostPreJournalIntake(t *testing.T) {
	runNativeQueueProducerRetryAfterLostPreJournalIntake(t, newSharedMemoryFixtureShutdown)
}

func TestMappedFileProducerRetryAfterLostPreJournalIntake(t *testing.T) {
	runNativeQueueProducerRetryAfterLostPreJournalIntake(t, newMappedFileFixtureShutdown)
}

func runNativeQueueProducerRetryAfterLostPreJournalIntake(t *testing.T, newFixture func(*testing.T, bool) nativeQueueFixture) {
	t.Helper()
	f := newFixture(t, false)
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
		Version: 1, RequestID: f.kind + "-prejournal-retry", Operation: core.Send,
		Target:   &core.Target{Kind: "room", Name: "#gateway-fixture"},
		Message:  f.kind + " intake retry 世界 " + strings.Repeat("λ", 900),
		IssuedAt: &issued, ExpiresAt: &expiry,
	}
	frame := sharedMemoryFrame(t, f, request, false)
	firstDelivery := sharedMemoryFragments(t, frame)
	if len(firstDelivery) < 2 {
		t.Fatalf("test intent did not require fragmented intake: %d fragment(s)", len(firstDelivery))
	}
	ingress, err := openNativeQueueView(f.kind, f.address)
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close()
	responseSlot, err := openNativeQueueView(f.kind, f.responseAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer responseSlot.Close()

	for i, fragment := range firstDelivery {
		writeCtx, writeCancel := context.WithTimeout(ctx, 3*time.Second)
		err := f.source.WriteMessage(writeCtx, fragment)
		writeCancel()
		if err != nil {
			t.Fatalf("publish initial fragment %d/%d: %v", i+1, len(firstDelivery), err)
		}
		waitSharedMemorySlotState(t, ingress, 0, 3*time.Second)
	}
	if got := sharedMemorySlotState(t, responseSlot); got != 0 {
		t.Fatalf("gateway published a response before durable intake completed: slot state=%d", got)
	}
	select {
	case <-f.process.done:
		t.Fatalf("gateway stopped before the deliberate kill: %v", f.process.err)
	default:
	}
	if err := f.process.command.Process.Kill(); err != nil {
		t.Fatalf("kill gateway during blocked journal intake: %v", err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("gateway exited cleanly after SIGKILL")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("killed gateway did not exit and join")
	}
	var jobs int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE request_id=?", request.RequestID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("held SQLite transaction observed pre-intake job count=%d err=%v", jobs, err)
	}
	if got := sharedMemorySlotState(t, responseSlot); got != 0 {
		t.Fatalf("killed pre-journal request left a response slot state=%d", got)
	}
	if records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(records) != 0 {
		t.Fatalf("pre-journal request reached native delivery: %+v", records)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false

	restarted := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, restarted)
	if address := gatewayAddress(t, restarted); address != f.address {
		t.Fatalf("gateway restart changed %s address: got %q want %q", f.kind, address, f.address)
	}
	retry := sharedMemoryFragments(t, frame)
	if len(retry) != len(firstDelivery) || string(retry[0][4:20]) == string(firstDelivery[0][4:20]) {
		t.Fatal("retry did not retain the signed frame under a new AGD1 delivery ID")
	}
	writeSharedMemoryFragments(t, f, retry)
	initial := readSharedMemoryResponse(t, f, time.Now().Add(5*time.Second))
	if initial.Result == nil || initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatalf("retry did not receive durable admission: %+v", initial)
	}
	receipt := pollSharedMemoryReceipt(t, f, request.RequestID)
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.RequestID != request.RequestID || receipt.MessageID == "" {
		t.Fatalf("retry receipt=%+v", receipt)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatalf("retry native event mismatch: %+v", seen)
	}
	records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != receipt.MessageID || records[0].From != "fixture-author" || records[0].Target != request.Target.Name || records[0].Message != request.Message {
		t.Fatalf("pre-journal retry duplicated or changed native intent: %+v", records)
	}
}
