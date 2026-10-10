//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func waitOSCOREClientCode(t *testing.T, path, code string, count int, deadline time.Time) {
	t.Helper()
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "v:1 ") && strings.Contains(line, " c:"+code+" ") {
				seen++
				if seen >= count {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native client did not decrypt expected code before renewed lifetime", code)
}

func TestOSCOREIncomingPartialTransferAbsoluteExpiry(t *testing.T) {
	f := newOSCOREFixture(t)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	seed := core.Request{Version: 1, RequestID: "oscore-input-expiry-seed", Operation: core.Send, Target: room, Message: "bootstrap before partial input", IssuedAt: &issued, ExpiresAt: &expires}
	_ = oscoreAccepted(t, f, seed)
	partial := seed
	partial.RequestID, partial.Message = "oscore-partial-input", strings.Repeat("p", 3500)
	relay := captureOSCORE(t, f.address)
	relay.holdResponse()
	command, sequence, _ := oscoreNativeCommand(t, f, oscoreInput(t, f, partial, 0), relay.front.LocalAddr().String(), oscoreSequence(t, f.sequence), 256)
	command.Args[2] = "7"
	logPath := filepath.Join(t.TempDir(), "native-client.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	command.Stdout, command.Stderr = log, log
	stop := startHeldOSCOREClient(t, command)
	first := oscoreResponseTime(t, relay)
	noOSCOREJob(t, f, partial.RequestID)
	if delay := time.Until(first.Add(1500 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	renewedUntil := time.Now().Add(3 * time.Second)
	relay.continueResponse(t)
	_ = oscoreResponseTime(t, relay)
	waitOSCOREClientCode(t, logPath, "2.31", 2, first.Add(2500*time.Millisecond))
	noOSCOREJob(t, f, partial.RequestID)
	if delay := time.Until(first.Add(3200 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	relay.continueResponse(t)
	_ = oscoreResponseTime(t, relay)
	// The authenticated missing-block refusal must reach the real client while a
	// renewed lifetime would still retain this unfinished receive body.
	waitOSCOREClientCode(t, logPath, "4.08", 1, renewedUntil)
	noOSCOREJob(t, f, partial.RequestID)
	stop()
	relay.close()
	resumeOSCORESequence(t, f, sequence)
	partial.RequestID, partial.Message = "oscore-after-partial-expiry", "healthy send after fixed receive expiration 🙂"
	accepted := oscoreAccepted(t, f, partial)
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 2 || native[1].ID != accepted.MessageID || native[1].From != "fixture-author" || native[1].Message != partial.Message {
		t.Fatal("expired input reached AIRC or prevented recovery", native)
	}
}
