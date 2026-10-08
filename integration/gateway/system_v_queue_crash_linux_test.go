//go:build linux && arm64

package gateway_test

import (
	"context"
	"database/sql"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"github.com/Someblueman/airc/pkg/irc"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSystemVQueueProducerRetryAfterUncertainDequeue(t *testing.T) {
	f := newSystemVFixtureShutdown(t, false)
	// The real journal writer holds admission before a protected request arrives.
	db, err := sql.Open("sqlite", filepath.Join(f.config.StateDir, "journal.sqlite")+"?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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
	if err := f.process.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.process.command.Process.Signal(syscall.SIGCONT) })
	waitPOSIXGatewayStopped(t, f.process.command.Process.Pid)
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "sv-uncertain-dequeue", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "retained producer intent 世界", IssuedAt: &issued, ExpiresAt: &expiry}
	parts := systemVFragments(t, systemVFrame(t, f, request, false))
	if len(parts) > 8 {
		t.Fatal("stopped ingress exceeds provisioned depth")
	}
	writeSystemVFragments(t, f, parts)
	if systemVQueuedBytes(t, f.source.Identity().ID) == 0 {
		t.Fatal("queued witness missing")
	}
	identity := f.source.Identity()
	if err := f.process.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for systemVQueuedBytes(t, f.source.Identity().ID) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if systemVQueuedBytes(t, f.source.Identity().ID) != 0 {
		t.Fatal("full request was not dequeued")
	}
	if err := f.process.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("kill unexpectedly exited cleanly")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("killed owner did not exit")
	}
	var jobs int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("dequeue admitted while journal writer held", jobs, err)
	}
	if systemVQueuedBytes(t, f.mailbox.Identity().ID) != 0 {
		t.Fatal("producer received admission response")
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	p := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, p)
	if addr := gatewayAddress(t, p); addr != f.address {
		t.Fatal("recovery changed queue key", addr)
	}
	recovered, err := sysvqueue.Open(identity.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Identity() != identity {
		t.Fatal("recovery replaced original ingress lifecycle")
	}
	_ = f.source.Close()
	f.source = recovered
	receipt := systemVAccepted(t, f, request)
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 {
		t.Fatal("producer retry receipt", receipt)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatal("retry observer mismatch", seen)
	}
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != receipt.MessageID || records[0].Message != request.Message {
		t.Fatal("uncertain dequeue retry duplicated/lost native intent", records)
	}
}

func systemVQueuedBytes(t *testing.T, id int) int {
	t.Helper()
	data, err := os.ReadFile("/proc/sysvipc/msg")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	header := strings.Fields(lines[0])
	idColumn, bytesColumn := -1, -1
	for i, name := range header {
		if name == "msqid" {
			idColumn = i
		}
		if name == "cbytes" {
			bytesColumn = i
		}
	}
	if idColumn < 0 || bytesColumn < 0 {
		t.Fatal("missing kernel queue accounting columns")
	}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != len(header) {
			t.Fatal("invalid kernel queue accounting row")
		}
		if fields[idColumn] == strconv.Itoa(id) {
			n, err := strconv.Atoi(fields[bytesColumn])
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatal("kernel queue accounting missing identifier", id)
	return 0
}
