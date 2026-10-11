//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func writeDTLSWatch(t *testing.T, f dtlsFixture, id string) {
	t.Helper()
	request := core.Request{Version: 1, RequestID: id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	writeDTLSFrame(t, f, frameRequest{Version: 1, CorrelationID: "dtls-" + id, Profile: "plain", Request: f.credentials.token(t, request, true), Stream: true})
}

func TestDTLSQuietWatchContiguousHeartbeatAndLeaseExpiry(t *testing.T) {
	lease := shortGatewayTimer(t, readLeaseTimer)
	f := newDTLSFixture(t)
	socket := f.config.Upstream.Address
	seedPinMutationMessage(t, socket, "#gateway-fixture", "dtls-quiet-retained-cursor")
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	started := time.Now()
	writeDTLSWatch(t, f, "dtls-quiet-watch")
	deadline := time.Now().Add(lease + 5*time.Second)
	first := readDTLSResponse(t, f.conn, deadline)
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || first.Page.Cursor == "" {
		t.Fatalf("DTLS first quiet page=%+v", first)
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
		response := readDTLSResponse(t, f.conn, deadline)
		if response.Sequence != sequence || response.CorrelationID != first.CorrelationID {
			t.Fatalf("DTLS quiet sequence=%+v want=%d", response, sequence)
		}
		if response.Event == "end" {
			if response.Reason != "lease_expired" {
				t.Fatalf("DTLS quiet terminal=%+v", response)
			}
			break
		}
		if response.Event != "page" || response.Page == nil || len(response.Page.Messages) != 0 || len(response.Page.Events) != 0 {
			t.Fatalf("DTLS heartbeat=%+v page=%+v first=%+v", response, response.Page, first.Page)
		}
		// Legacy watch reseals with a fresh AEAD nonce; compare authenticated
		// positions, not token bytes, to prove heartbeats never advance history.
		position, err := codec.Open(scope, response.Page.Cursor)
		if err != nil || position != initialPosition {
			t.Fatalf("DTLS heartbeat position=%q want=%q err=%v", position, initialPosition, err)
		}
	}
	if elapsed := time.Since(started); elapsed < lease-2*time.Second || elapsed > lease+3*time.Second {
		t.Fatalf("DTLS quiet lease=%s", elapsed)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	dtlsDatagramCall(t, f, core.Request{Version: 1, RequestID: "dtls-after-quiet", Operation: core.CapabilitiesGet}, true)
}
