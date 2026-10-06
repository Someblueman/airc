//go:build darwin || linux

package gateway_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMQTTSNInterruptedWriterNeedsProducerRetry(t *testing.T) {
	f := prepareMQTTSNFixture(t)
	f.start(t)
	client := f.connect(t)
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
	r := core.Request{Version: 1, RequestID: "mqtt-sn-held-writer", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "retry after MQTT-SN pre-intake interruption 🙂"}
	payload := f.credentials.token(t, r, true)
	mqttSNSend(t, client, mqttSNPublication(7, payload, false))
	mqttSNNoReply(t, client)
	noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
	if err := f.process.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal(f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer-blocked service did not join")
	}
	if _, err := lock.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, r.Target.Name); len(native) != 0 {
		t.Fatal("uncommitted publication reached native", native)
	}
	f.start(t)
	client = f.connect(t)
	mqttSNSend(t, client, mqttSNPublication(7, payload, true))
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	mqttSNAwaitAccepted(t, f, r.RequestID)
	f.stop(t)
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("journal not released", err)
	}
	defer s.Close()
	job, err := s.Lookup(t.Context(), fixtureIdentity(t, f.config), r.RequestID)
	if err != nil || !job.Result.Durable || job.Result.SendAttempts != 1 || job.Result.MessageID != event.ID || job.Request.Message != r.Message {
		t.Fatal("producer retry did not recover", job, err)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, r.Target.Name); len(native) != 1 || native[0].ID != event.ID || native[0].Message != r.Message {
		t.Fatal("wrong recovered native delivery", native)
	}
}
