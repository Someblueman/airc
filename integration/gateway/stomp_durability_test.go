//go:build darwin || linux

package gateway_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSTOMPReceiptWaitsForCommittedIntake(t *testing.T) {
	f := prepareSTOMPFixture(t)
	f.start(t)
	peer := f.connect(t)
	db, err := sql.Open("sqlite", filepath.Join(f.config.StateDir, "journal.sqlite")+"?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lock, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = lock.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	r := core.Request{Version: 1, RequestID: "stomp-blocked-intake", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "STOMP blocked durable intake\n世界"}
	peer.submit(t, stompPlainBody(t, r))
	type readResult struct {
		frame sqFrame
		err   error
	}
	ack := make(chan readResult, 1)
	if err := peer.conn.SetReadDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() { frame, err := sqDecode(peer.reader); ack <- readResult{frame, err} }()
	finished := false
	defer func() {
		if !finished {
			_ = peer.conn.NetConn().Close()
			<-ack
		}
	}()
	select {
	case result := <-ack:
		finished = true
		t.Fatal("receipt preceded committed intake", result)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := lock.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	select {
	case result := <-ack:
		finished = true
		if result.err != nil || result.frame.command != "RECEIPT" || result.frame.headers["receipt-id"] != "intake" {
			t.Fatal(result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no committed receipt")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", r.RequestID).Scan(&count); err != nil || count != 1 {
		t.Fatal("receipt without committed row", count, err)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	f.stop(t)
}
