//go:build darwin || linux

package service

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestIRCServicePreparationDispatchAndJoinedShutdown(t *testing.T) {
	published := make(chan struct{})
	c := ircServiceFixture(t, func(conn *tls.Conn) {
		stamp := time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000Z")
		if _, err := io.WriteString(conn, "@account=alice;msgid=service;time="+stamp+" :alice!u@h PRIVMSG #external :whole service 世界 body  \r\n"); err != nil {
			t.Error(err)
		}
		if _, err := io.WriteString(conn, "PING :after-intake\r\n"); err != nil {
			t.Error(err)
			return
		}
		command, err := ircwire.NewReader(conn).Read()
		if err != nil || command.Name != "PONG" {
			t.Error("intake fence", command, err)
			return
		}
		close(published)
	})
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
	t.Cleanup(func() { cancel(); _ = reader.Close(); <-finished })
	var ready struct {
		State   string
		Brokers []struct{ ID, Protocol string }
	}
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || ready.State != "ready" || len(ready.Brokers) != 1 || ready.Brokers[0].Protocol != "irc" || ready.Brokers[0].ID != "network" {
		t.Fatal("ready", ready, err)
	}
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("service did not connect/join")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service not joined")
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("journal not released", err)
	}
	defer journal.Close()
	// The services account "alice" posts as the guest irc-alice.
	job, err := journal.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "irc-alice"}}, ircServiceRequestID("service"))
	if err != nil || job.Request.Message != "whole service 世界 body  " {
		t.Fatal("whole service intent", job, err)
	}
}

func ircServiceRequestID(id string) string {
	h := sha256.New()
	for _, part := range []string{"airc-irc-event-v1", "network", id} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
