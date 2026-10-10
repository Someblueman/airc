//go:build darwin || linux

package gateway_test

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUARTNativeTLSAuthSendBoundedHistoryReplayAndJoinedShutdown(t *testing.T) {
	f := newUARTFixture(t)
	for key, valid := range f.pty.inspect(t) {
		if !valid {
			t.Fatalf("PTY link did not use fixed raw 115200 8N1, no flow (%s)", key)
		}
	}
	caps := core.Request{Version: 1, RequestID: "uart-split-capabilities", Operation: core.CapabilitiesGet}
	writeSplitUARTFrame(t, f.connection, adapter.FrameRequest{Version: 1, CorrelationID: "uart-split-capabilities", Profile: "encrypted", Envelope: f.credentials.token(t, caps, true)})
	if response := readFrame(t, f.connection); response.CorrelationID != "uart-split-capabilities" || response.Error != nil || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatal("UART failed a frame split across TLS writes", response)
	}
	writeFrame(t, f.connection, adapter.FrameRequest{Version: 1, CorrelationID: "uart-bad-auth", Profile: "encrypted", Envelope: "invalid"})
	if response := readFrame(t, f.connection); response.CorrelationID != "uart-bad-auth" || response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil {
		t.Fatal("UART accepted invalid AIRC credentials", response)
	}

	framedAuthenticatedSendAndBoundedResume(t, f.connection, f.credentials, f.socket, true)
	watch := core.Request{Version: 1, RequestID: "uart-shutdown-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	writeFrame(t, f.connection, adapter.FrameRequest{Version: 1, CorrelationID: "uart-shutdown-watch", Profile: "encrypted", Envelope: f.credentials.token(t, watch, true), Stream: true})
	if response := readFrame(t, f.connection); response.Event != "page" || response.Page == nil {
		t.Fatal("UART watch did not open before shutdown", response)
	}
	f.process.stop(t)
	settings := f.pty.query(t, "restored")
	if !settings["restored"] || !settings["lock_free"] {
		t.Fatal("UART shutdown did not restore tty settings and release flock", settings)
	}
	_ = f.connection.Close()
	messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if len(messages) != 2 {
		t.Fatalf("native observer records=%d, want 2", len(messages))
	}
	for _, message := range messages {
		if message.From != "fixture-author" || message.Target != "#gateway-fixture" {
			t.Fatal("UART native author/target mismatch", message)
		}
	}
}

func TestUARTRejectsOversizedFramedRequest(t *testing.T) {
	f := newUARTFixture(t)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], adapter.MaxRequestFrameBytes+1)
	if _, err := f.connection.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	_ = f.connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, readErr := f.connection.Read(make([]byte, 1))
	if readErr == nil {
		t.Fatal("UART kept an oversized framed request open")
	}
	if timeout, ok := readErr.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		t.Fatal("UART oversized-frame refusal was only a read timeout", readErr)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("UART exited successfully after oversized request")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("UART service did not end the malformed session")
	}
	settings := f.pty.query(t, "restored")
	if !settings["restored"] || !settings["lock_free"] {
		t.Fatal("malformed UART session did not restore tty settings and release flock", settings)
	}
	rpcNoJobs(t, f.config.StateDir)
	messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if len(messages) != 0 {
		t.Fatal("oversized UART frame reached native AIRC", messages)
	}
}

func TestUARTReturnsLargeWholeBoundedHistoryFrame(t *testing.T) {
	f := newUARTFixture(t)
	bodies := seedLargeUARTHistory(t, f)
	request := core.Request{Version: 1, RequestID: "uart-large-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 32, Start: "retained"}
	writeFrame(t, f.connection, adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	response := readFrame(t, f.connection)
	if response.Error != nil || response.Result == nil || response.Result.Page == nil {
		t.Fatalf("UART large bounded history failed: response=%+v error=%+v", response, response.Error)
	}
	page := response.Result.Page
	if len(page.Messages) != len(bodies) || page.Gap || page.Status != "ok" || page.Cursor == "" {
		t.Fatalf("UART large history page=%+v", page)
	}
	for i, message := range page.Messages {
		if message.Message != bodies[i] || message.From != "uart-seeder" || message.Target != "#gateway-fixture" {
			t.Fatalf("UART large history record %d mismatch", i)
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) < 48<<10 || len(encoded) > adapter.MaxResponseFrameBytes {
		t.Fatalf("UART response size=%d, want a complete frame between 48 KiB and %d bytes; marshal err=%v", len(encoded), adapter.MaxResponseFrameBytes, err)
	}
	f.process.stop(t)
}

func seedLargeUARTHistory(t *testing.T, f *uartFixture) []string {
	t.Helper()
	sender := dial(t, f.socket, "uart-seeder", "", false, false)
	defer sender.Close()
	if err := sender.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, f.observer, func(e *irc.JoinEvent) bool { return e.Agent == "uart-seeder" })
	bodies := make([]string, 24)
	for i := range bodies {
		bodies[i] = strings.Repeat(string(rune('a'+i)), 1600) + string(rune('A'+i))
		if err := sender.Send("#gateway-fixture", bodies[i]); err != nil {
			t.Fatal(err)
		}
		seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == bodies[i] })
		if seen.From != "uart-seeder" || seen.Target != "#gateway-fixture" || seen.Message != bodies[i] {
			t.Fatal("native observer mismatch for large history seed", seen)
		}
	}
	return bodies
}

func TestUARTShutdownCancelsBackpressuredLargeResponse(t *testing.T) {
	f := newUARTFixture(t)
	// Drain a real short TLS response and any handshake records before measuring
	// output queued by the large history request.
	caps := core.Request{Version: 1, RequestID: "uart-cancel-capabilities", Operation: core.CapabilitiesGet}
	writeFrame(t, f.connection, adapter.FrameRequest{Version: 1, CorrelationID: caps.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, caps, true)})
	if response := readFrame(t, f.connection); response.Error != nil || response.Result == nil {
		t.Fatal("UART capability probe failed", response)
	}
	f.pty.waitOutputDrained(t)
	seedLargeUARTHistory(t, f)
	request := core.Request{Version: 1, RequestID: "uart-blocked-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 32, Start: "retained"}
	writeFrame(t, f.connection, adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	f.pty.waitWriteBlocked(t)
	f.process.stop(t)
	settings := f.pty.query(t, "restored")
	if !settings["restored"] || !settings["lock_free"] {
		t.Fatal("blocked-write cancellation did not restore tty settings and release flock", settings)
	}
	_ = f.connection.Close()
}
