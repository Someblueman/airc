//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testmqueue"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPOSIXQueueWholeHistoryLiveWatchAndJoinedShutdown(t *testing.T) {
	testmqueue.Require(t)
	f := newPOSIXFixture(t)
	socket, room := f.config.Upstream.Address, "#gateway-fixture"
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	t.Cleanup(func() { _ = author.Close() })
	for i := 0; i < 6; i++ {
		id := "ug-native-" + string(rune('a'+i))
		if err := author.SendWithID(room, "posix native "+strings.Repeat("x", 3500), id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	native := nativeWebTransportMessages(t, socket, room)
	request := core.Request{Version: 1, RequestID: "ug-large-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: room}, Start: "retained", Limit: 8}
	history := posixCall(t, f, request)
	if history.Result.Page == nil || !reflect.DeepEqual(history.Result.Page.Messages, native) {
		t.Fatalf("POSIX queue whole history=%+v", history)
	}
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	request.RequestID, request.Operation = "ug-live-watch", core.Watch
	writePOSIXFragments(t, f, posixFragments(t, posixFrame(t, f, request, true)))
	first := readPOSIXResponse(t, f, time.Now().Add(3*time.Second))
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil || !reflect.DeepEqual(first.Page.Messages, native) {
		t.Fatalf("POSIX queue whole retained watch=%+v", first)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	const liveBody = "POSIX queue actual live update"
	if err := author.SendWithID(room, liveBody, "ug-live-update"); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "ug-live-update" })
	live := readPOSIXResponse(t, f, time.Now().Add(3*time.Second))
	if live.Event != "page" || live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) != 1 {
		t.Fatalf("POSIX queue live=%+v", live)
	}
	all := nativeWebTransportMessages(t, socket, room)
	expected := *all[len(all)-1]
	expected.Seq, expected.Receipt = 0, nil
	if !reflect.DeepEqual(live.Page.Messages[0], &expected) {
		t.Fatalf("POSIX queue live differs from native: got=%+v want=%+v", live.Page.Messages[0], expected)
	}
	// A partially assembled mutation must not survive or admit at shutdown.
	partial := core.Request{Version: 1, RequestID: "ug-shutdown-partial", Operation: core.Send, Target: request.Target, Message: strings.Repeat("p", 3500)}
	fragments := posixFragments(t, posixFrame(t, f, partial, false))
	if len(fragments) < 2 {
		t.Fatal("partial witness did not fragment")
	}
	writePOSIXFragments(t, f, fragments[:1])
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("POSIX queue SIGINT: %v %s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("POSIX queue shutdown did not join")
	}
	waitNativeLeaseGone(t, statusClient, baseline)
	if _, err := os.Lstat("/dev/mqueue" + f.address); !os.IsNotExist(err) {
		t.Fatalf("owned ingress not removed: %v", err)
	}
	if _, err := os.Lstat("/dev/mqueue" + f.mailbox.Name()); err != nil {
		t.Fatalf("client mailbox removed: %v", err)
	}
	rebound, err := adapter.CreatePOSIXQueue(f.address, 8)
	if err != nil {
		t.Fatalf("POSIX queue rebind: %v", err)
	}
	_ = rebound.Close()
	rpcNoJobs(t, f.config.StateDir)
}
