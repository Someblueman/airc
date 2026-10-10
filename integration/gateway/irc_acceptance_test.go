//go:build darwin || linux

package gateway_test

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestIRCServiceNativeWholeSendReplayAndJoinedShutdown(t *testing.T) {
	f, connected := ircNativeFixture(t)
	configureIRCReceiptListener(t, f)
	process := startGateway(t, f.binary, f.config)
	select {
	case data := <-process.ready:
		var ready struct {
			State   string
			Brokers []struct{ ID, Protocol string }
		}
		if err := json.Unmarshal(data, &ready); err != nil || ready.State != "ready" || len(ready.Brokers) != 1 || ready.Brokers[0].Protocol != "irc" {
			t.Fatal("IRC ready", string(data), err)
		}
	case <-process.done:
		t.Fatal("gateway exited", process.err, process.stderr.String())
	case <-time.After(8 * time.Second):
		t.Fatal("gateway not ready")
	}
	var peer *tls.Conn
	select {
	case peer = <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("IRC registration/join missing")
	}
	stamp := time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000Z")
	write := func(id, nick, body string) {
		t.Helper()
		if _, err := io.WriteString(peer, "@account=alice;msgid="+id+";time="+stamp+" :"+nick+"!u@h PRIVMSG #external :"+body+"\r\n"); err != nil {
			t.Fatal(err)
		}
	}
	body := "IRC native whole 世界 body  "
	write("original", "alice", body)
	first := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
	if first.From != "fixture-author" || first.Target != "#gateway-fixture" {
		t.Fatal(first)
	}
	write("original", "renamed", body)
	write("fence", "alice", "IRC replay fence")
	fence := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "IRC replay fence" })
	if fence.From != "fixture-author" || fence.Target != "#gateway-fixture" {
		t.Fatal(fence)
	}
	waitIRCNativeAccepted(t, f, "original", first.ID)
	waitIRCNativeAccepted(t, f, "fence", fence.ID)
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
		if process.err != nil {
			t.Fatal(process.err, process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("IRC service not joined")
	}
	journal, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("journal not released", err)
	}
	defer journal.Close()
	for _, item := range []struct{ id, messageID string }{{"original", first.ID}, {"fence", fence.ID}} {
		job, err := journal.Lookup(t.Context(), fixtureIdentity(t, f.config), ircNativeRequestID(item.id))
		if err != nil || !job.Result.Durable || job.Result.MessageID != item.messageID || job.Result.SendAttempts != 1 {
			t.Fatal("native replay", job, err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	process = startGateway(t, f.binary, f.config)
	select {
	case data := <-process.ready:
		var ready struct{ State string }
		if err := json.Unmarshal(data, &ready); err != nil || ready.State != "ready" {
			t.Fatal("restart ready", string(data), err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("restart not ready")
	}
	select {
	case peer = <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not join")
	}
	write("original", "after-restart", body)
	write("final", "alice", "IRC restart fence")
	final := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "IRC restart fence" })
	if final.From != "fixture-author" || final.Target != "#gateway-fixture" {
		t.Fatal(final)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
		if process.err != nil {
			t.Fatal(process.err, process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restarted service not joined")
	}
	messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if len(messages) != 3 {
		t.Fatal("IRC replay duplicated posts", messages)
	}
}

func ircNativeRequestID(id string) string {
	h := sha256.New()
	for _, part := range []string{"airc-irc-event-v1", "network", id} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
