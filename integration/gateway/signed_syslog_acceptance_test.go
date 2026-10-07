//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestSignedSyslogNativeCoveredBatchReplayAndRestart(t *testing.T) {
	f := prepareSignedSyslogFixture(t)
	f.start(t)
	records := [][]byte{
		[]byte(signedSyslogHeader(time.Now()) + "- signed exact 世界 🙂"),
		[]byte(signedSyslogHeader(time.Now()) + "- signed second\nline"),
	}
	block := f.block(t, 0, 1, records...)
	peer := f.connect(t)
	f.certificate(t, peer)
	for _, raw := range records {
		syslogNativeWrite(t, peer, raw)
	}
	syslogNativeWrite(t, peer, block)
	ids := make([]string, len(records))
	bodies := []string{"signed exact 世界 🙂", "signed second\nline"}
	for range bodies {
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == bodies[0] || e.Message == bodies[1] })
		i := 0
		if event.Message == bodies[1] {
			i = 1
		}
		if ids[i] != "" || event.From != "fixture-author" || event.Target != "#gateway-fixture" {
			t.Fatal(event)
		}
		ids[i] = event.ID
		f.acceptedID(t, f.requestID(t, uint64(i+1), records[i]), event.ID)
	}
	peer.NetConn().Close()
	for phase := 0; phase < 2; phase++ {
		if phase == 1 {
			f.stop(t)
			f.start(t)
		}
		peer = f.connect(t)
		f.certificate(t, peer)
		for _, raw := range records {
			syslogNativeWrite(t, peer, raw)
		}
		syslogNativeWrite(t, peer, block)
		body := "signed reconnect fence"
		if phase == 1 {
			body = "signed restart fence"
		}
		raw := []byte(signedSyslogHeader(time.Now()) + "- " + body)
		syslogNativeWrite(t, peer, raw)
		syslogNativeWrite(t, peer, f.block(t, phase+1, phase+3, raw))
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
		f.acceptedID(t, f.requestID(t, uint64(phase+3), raw), event.ID)
		peer.NetConn().Close()
	}
	f.stop(t)
	messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if len(messages) != 4 {
		t.Fatal("signed replay duplicated native posts", messages)
	}
	for _, id := range ids {
		found := false
		for _, message := range messages {
			if message.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatal("replay changed message identity", id)
		}
	}
}
