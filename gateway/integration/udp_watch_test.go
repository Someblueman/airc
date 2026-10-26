//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func writeUDPWatch(t *testing.T, f udpFixture, id string) {
	t.Helper()
	request := core.Request{Version: 1, RequestID: id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	writeUDPFrame(t, f, frameRequest{Version: 1, CorrelationID: "udp-" + id, Profile: "plain", Request: f.credentials.token(t, request, true), Stream: true})
}

func TestUDPQuietWatchHeartbeatCursorAndLeaseExpiry(t *testing.T) {
	lease := shortGatewayTimer(t, readLeaseTimer)
	f := newUDPFixture(t)
	socket := f.config.Upstream.Address
	seedPinMutationMessage(t, socket, "#gateway-fixture", "udp-quiet-retained-cursor")
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	started := time.Now()
	writeUDPWatch(t, f, "udp-quiet-watch")
	deadline := time.Now().Add(lease + 5*time.Second)
	first := readUDPResponse(t, f, deadline)
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || first.Page.Cursor == "" {
		t.Fatalf("UDP first quiet page=%+v", first)
	}
	codec, err := auth.NewCursorCodec(f.config.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	scope := auth.CursorScope{Realm: f.config.Realm, Author: fixtureIdentity(t, f.config).Author.Scope(), Operation: core.Watch, Target: core.Target{Kind: "room", Name: "#gateway-fixture"}}
	initialPosition, err := codec.Open(scope, first.Page.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	for sequence := uint64(2); ; sequence++ {
		response := readUDPResponse(t, f, deadline)
		if response.Sequence != sequence || response.CorrelationID != first.CorrelationID {
			t.Fatalf("UDP quiet sequence=%+v want=%d", response, sequence)
		}
		if response.Event == "end" {
			if response.Reason != "lease_expired" {
				t.Fatalf("UDP quiet terminal=%+v", response)
			}
			break
		}
		if response.Event != "page" || response.Page == nil || len(response.Page.Messages) != 0 || len(response.Page.Events) != 0 {
			t.Fatalf("UDP heartbeat=%+v page=%+v first=%+v", response, response.Page, first.Page)
		}
		position, err := codec.Open(scope, response.Page.Cursor)
		if err != nil || position != initialPosition {
			t.Fatalf("UDP heartbeat position=%q want=%q err=%v", position, initialPosition, err)
		}
	}
	if elapsed := time.Since(started); elapsed < lease-2*time.Second || elapsed > lease+3*time.Second {
		t.Fatalf("UDP quiet lease=%s", elapsed)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "udp-after-quiet", Operation: core.CapabilitiesGet}, true)
}
