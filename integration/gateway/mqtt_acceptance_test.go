//go:build darwin || linux

package gateway_test

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMQTTDurablePUBACKNativeReplayAndJoinedShutdown(t *testing.T) {
	f := newMQTTFixture(t)
	client := f.connect(t)
	r := core.Request{Version: 1, RequestID: "mqtt-native", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "MQTT exact native body\n世界 🙂"}
	payload := mqttRequest(t, r)
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
	mqttWrite(t, client, mqttPublication(7, "airc/send", payload, false))
	ack := make(chan error, 1)
	go func() {
		var reply [4]byte
		_, err := io.ReadFull(client, reply[:])
		if err == nil && reply != [4]byte{0x40, 2, 0, 7} {
			err = io.ErrUnexpectedEOF
		}
		ack <- err
	}()
	select {
	case err := <-ack:
		t.Fatal("PUBACK preceded durable admission", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := lock.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	select {
	case err := <-ack:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no durable PUBACK")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", r.RequestID).Scan(&count); err != nil || count != 1 {
		t.Fatal("PUBACK without committed row", count, err)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	// A connection with the same client ID replaces the old one before CONNACK.
	replacement := f.connect(t)
	mqttPeerClosed(t, client)
	mqttWrite(t, replacement, mqttPublication(8, "airc/send", payload, true))
	mqttReply(t, replacement, []byte{0x40, 2, 0, 8})
	// An unread PUBACK is acknowledgement loss to the producer. Replay the exact
	// request on a fresh connection with a different packet identifier.
	lost := r
	lost.RequestID, lost.Message = "mqtt-lost-first-ack", "first PUBACK unread\nUnicode Ω 🙂"
	lostPayload := mqttRequest(t, lost)
	mqttWrite(t, replacement, mqttPublication(9, "airc/send", lostPayload, false))
	lostEvent := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == lost.Message })
	if lostEvent.From != "fixture-author" || lostEvent.Target != lost.Target.Name {
		t.Fatal(lostEvent)
	}
	_ = replacement.NetConn().Close()
	reconnected := f.connect(t)
	mqttWrite(t, reconnected, mqttPublication(1, "airc/send", lostPayload, true))
	mqttReply(t, reconnected, []byte{0x40, 2, 0, 1})
	f.stop(t)
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("journal not released", err)
	}
	defer s.Close()
	job, err := s.LookupAuthor(t.Context(), fixtureAuthorSubmission(t), r.RequestID)
	if err != nil || !job.Result.Durable || job.Result.MessageID != event.ID || job.Result.SendAttempts != 1 || job.Request.Message != r.Message {
		t.Fatal("stable durable MQTT intent", job, err)
	}
	job, err = s.LookupAuthor(t.Context(), fixtureAuthorSubmission(t), lost.RequestID)
	if err != nil || !job.Result.Durable || job.Result.MessageID != lostEvent.ID || job.Result.SendAttempts != 1 || job.Request.Message != lost.Message {
		t.Fatal("lost first PUBACK replay", job, err)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, r.Target.Name); len(native) != 2 || native[0].ID != event.ID || native[0].Message != r.Message || native[1].ID != lostEvent.ID || native[1].Message != lost.Message {
		t.Fatal("MQTT duplicated native delivery", native)
	}
}
