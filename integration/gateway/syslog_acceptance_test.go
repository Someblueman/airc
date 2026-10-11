//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSyslogNativeExactAuthorTargetBodyAndReplay(t *testing.T) {
	f := prepareSyslogFixture(t)
	f.start(t)
	peer := f.connect(t)
	var records [][]byte
	var ids []string
	for i := 0; i < 3; i++ {
		body := fmt.Sprintf("syslog event %d exact body\n世界 🙂", i)
		raw := syslogNativeRecord(fmt.Sprintf("event%d", i), body)
		syslogNativeWrite(t, peer, raw)
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
		if event.From != "fixture-author" || event.Target != "#gateway-fixture" {
			t.Fatal(event)
		}
		f.accepted(t, raw, event.ID)
		records = append(records, raw)
		ids = append(ids, event.ID)
	}
	peer.NetConn().Close()
	peer = f.connect(t)
	for _, raw := range records {
		syslogNativeWrite(t, peer, raw)
	}
	// A new later record fences prior one-way records without inventing an ACK.
	fence := syslogNativeRecord("fence", "syslog reconnect fence")
	syslogNativeWrite(t, peer, fence)
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "syslog reconnect fence" })
	f.accepted(t, fence, event.ID)
	records = append(records, fence)
	ids = append(ids, event.ID)
	f.stop(t)
	journal, e := store.Open(t.Context(), f.config)
	if e != nil {
		t.Fatal(e)
	}
	for i, raw := range records {
		job, e := journal.LookupAuthor(t.Context(), fixtureAuthorSubmission(t), syslogNativeID(t, f, raw))
		if e != nil || !job.Result.Durable || job.Result.MessageID != ids[i] || job.Result.SendAttempts != 1 {
			t.Fatal(job, e)
		}
	}
	if e := journal.Close(); e != nil {
		t.Fatal(e)
	}
	f.start(t)
	peer = f.connect(t)
	for _, raw := range records {
		syslogNativeWrite(t, peer, raw)
	}
	final := syslogNativeRecord("final", "syslog restart fence")
	syslogNativeWrite(t, peer, final)
	event = next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "syslog restart fence" })
	f.accepted(t, final, event.ID)
	f.stop(t)
	messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if len(messages) != 5 {
		t.Fatal("replay duplicated native posts", messages)
	}
	for i, id := range ids {
		if messages[i].ID != id || messages[i].From != "fixture-author" || messages[i].Target != "#gateway-fixture" {
			t.Fatal(messages[i])
		}
	}
}
