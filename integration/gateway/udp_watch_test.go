//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func writeUDPWatch(t *testing.T, f udpFixture, id string) {
	t.Helper()
	request := core.Request{Version: 1, RequestID: id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	writeUDPFrame(t, f, frameRequest{Version: 1, CorrelationID: "udp-" + id, Profile: "plain", Request: f.credentials.token(t, request, true), Stream: true})
}

func TestUDPWatchRetainedAndLiveMetadata(t *testing.T) {
	f := newUDPFixture(t)
	socket, room := f.config.Upstream.Address, "#gateway-fixture"
	seedPinMutationMessage(t, socket, room, "udp-retained-native-record")
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	writeUDPWatch(t, f, "udp-live-watch")
	first := readUDPResponse(t, f, time.Now().Add(3*time.Second))
	if first.Event != "page" || first.Sequence != 1 || first.CorrelationID != "udp-udp-live-watch" || first.Page == nil || !reflect.DeepEqual(first.Page.Messages, nativeWebTransportMessages(t, socket, room)) {
		t.Fatalf("UDP retained native page=%+v", first)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	author := dial(t, socket, "udp-watch-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	const body = "UDP actual live watch 🙂"
	if err := author.SendWithID(room, body, "udp-live-update"); err != nil {
		t.Fatal(err)
	}
	live := readUDPResponse(t, f, time.Now().Add(3*time.Second))
	if live.Event != "page" || live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) != 1 || live.Page.Messages[0].Message != body || live.Page.Messages[0].From != "udp-watch-author" || live.Page.Messages[0].Target != room || live.Page.Messages[0].ID == "" {
		t.Fatalf("UDP live page=%+v", live)
	}
	native := nativeWebTransportMessages(t, socket, room)
	expected := *native[len(native)-1]
	if live.Page.Messages[0].Seq != 0 || live.Page.Messages[0].Receipt != nil || expected.Seq == 0 {
		t.Fatalf("live/CHECK sequence availability live=%+v native=%+v", live.Page.Messages[0], expected)
	}
	expected.Seq, expected.Receipt = 0, nil
	if !reflect.DeepEqual(live.Page.Messages[0], &expected) {
		t.Fatalf("UDP whole live record=%+v native CHECK=%+v", live.Page.Messages[0], native[len(native)-1])
	}
}

func TestUDPQuietWatchHeartbeatCursorAndLeaseExpiry(t *testing.T) {
	f := newUDPFixture(t)
	socket := f.config.Upstream.Address
	seedPinMutationMessage(t, socket, "#gateway-fixture", "udp-quiet-retained-cursor")
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	started := time.Now()
	writeUDPWatch(t, f, "udp-quiet-watch")
	deadline := time.Now().Add(20 * time.Second)
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
	if elapsed := time.Since(started); elapsed < 13*time.Second || elapsed > 18*time.Second {
		t.Fatalf("UDP quiet lease=%s", elapsed)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "udp-after-quiet", Operation: core.CapabilitiesGet}, true)
}
