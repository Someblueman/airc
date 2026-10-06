//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUnixgramWholeHistoryLiveWatchAndJoinedShutdown(t *testing.T) {
	f := newUnixgramFixture(t)
	socket, room := f.config.Bindings[0].Address, "#gateway-fixture"
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	t.Cleanup(func() { _ = author.Close() })
	for i := 0; i < 6; i++ {
		id := "ug-native-" + string(rune('a'+i))
		if err := author.SendWithID(room, "unixgram native "+strings.Repeat("x", 3500), id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	native := nativeWebTransportMessages(t, socket, room)
	request := core.Request{Version: 1, RequestID: "ug-large-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: room}, Start: "retained", Limit: 8}
	history := unixgramCall(t, f, request, true)
	if history.Result.Page == nil || !reflect.DeepEqual(history.Result.Page.Messages, native) {
		t.Fatalf("Unix datagram whole history=%+v", history)
	}
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	request.RequestID, request.Operation = "ug-live-watch", core.Watch
	writeUnixgramFragments(t, f, unixgramFragments(t, unixgramFrame(t, f, request, true)))
	first := readUnixgramResponse(t, f, time.Now().Add(3*time.Second))
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || !reflect.DeepEqual(first.Page.Messages, native) {
		t.Fatalf("Unix datagram whole retained watch=%+v", first)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	const liveBody = "Unix datagram actual live update"
	if err := author.SendWithID(room, liveBody, "ug-live-update"); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "ug-live-update" })
	live := readUnixgramResponse(t, f, time.Now().Add(3*time.Second))
	if live.Event != "page" || live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) != 1 {
		t.Fatalf("Unix datagram live=%+v", live)
	}
	all := nativeWebTransportMessages(t, socket, room)
	expected := *all[len(all)-1]
	expected.Seq, expected.Receipt = 0, nil
	if !reflect.DeepEqual(live.Page.Messages[0], &expected) {
		t.Fatalf("Unix datagram live differs from native: got=%+v want=%+v", live.Page.Messages[0], expected)
	}
	// A partially assembled mutation must not survive or admit at shutdown.
	partial := core.Request{Version: 1, RequestID: "ug-shutdown-partial", Operation: core.Send, Target: request.Target, Message: strings.Repeat("p", 3500)}
	fragments := unixgramFragments(t, unixgramFrame(t, f, partial, false))
	if len(fragments) < 2 {
		t.Fatal("partial witness did not fragment")
	}
	writeUnixgramFragments(t, f, fragments[:1])
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("Unix datagram SIGINT: %v %s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Unix datagram shutdown did not join")
	}
	waitNativeLeaseGone(t, statusClient, baseline)
	if _, err := os.Lstat(f.address); !os.IsNotExist(err) {
		t.Fatalf("owned ingress not removed: %v", err)
	}
	if _, err := os.Lstat(f.mailbox.LocalAddr().String()); err != nil {
		t.Fatalf("client mailbox removed: %v", err)
	}
	rebound, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: f.address, Net: "unixgram"})
	if err != nil {
		t.Fatalf("Unix datagram rebind: %v", err)
	}
	_ = rebound.Close()
	rpcNoJobs(t, f.config.StateDir)
}

func TestUnixgramQuietWatchHeartbeatAndLeaseExpiry(t *testing.T) {
	f := newUnixgramFixture(t)
	socket := f.config.Bindings[0].Address
	seedPinMutationMessage(t, socket, "#gateway-fixture", "unixgram-quiet-native")
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	request := core.Request{Version: 1, RequestID: "ug-quiet", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	started, deadline := time.Now(), time.Now().Add(20*time.Second)
	writeUnixgramFragments(t, f, unixgramFragments(t, unixgramFrame(t, f, request, true)))
	first := readUnixgramResponse(t, f, deadline)
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || first.Page.Cursor == "" {
		t.Fatalf("Unix datagram quiet initial=%+v", first)
	}
	codec, err := auth.NewCursorCodec(f.config.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	principal := f.config.Principals[0]
	scope := auth.CursorScope{Realm: f.config.Realm, Principal: principal.ID, Binding: principal.Binding, Operation: core.Watch, Target: *request.Target}
	position, err := codec.Open(scope, first.Page.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	heartbeats := 0
	for sequence := uint64(2); ; sequence++ {
		response := readUnixgramResponse(t, f, deadline)
		if response.CorrelationID != first.CorrelationID || response.Sequence != sequence {
			t.Fatalf("Unix datagram quiet sequence=%+v", response)
		}
		if response.Event == "end" {
			if response.Reason != "lease_expired" {
				t.Fatalf("Unix datagram quiet terminal=%+v", response)
			}
			break
		}
		if response.Event != "page" || response.Page == nil || len(response.Page.Messages) != 0 || len(response.Page.Events) != 0 {
			t.Fatalf("Unix datagram quiet heartbeat=%+v", response)
		}
		current, err := codec.Open(scope, response.Page.Cursor)
		if err != nil || current != position {
			t.Fatalf("Unix datagram quiet cursor=%q want=%q err=%v", current, position, err)
		}
		heartbeats++
	}
	if elapsed := time.Since(started); heartbeats == 0 || elapsed < 13*time.Second || elapsed > 18*time.Second {
		t.Fatalf("Unix datagram quiet lease elapsed=%s heartbeats=%d", elapsed, heartbeats)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	unixgramCall(t, f, core.Request{Version: 1, RequestID: "ug-after-quiet", Operation: core.CapabilitiesGet}, true)
}
