//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSocketPairAcceptedReplayAfterFreshChild(t *testing.T) {
	localStreamAcceptedReplayAfterFreshChild(t, "socketpair")
}

func TestInheritedUnixAcceptedReplayAfterFreshChild(t *testing.T) {
	localStreamAcceptedReplayAfterFreshChild(t, "inherited")
}

func TestFIFOAcceptedReplayAfterFreshChild(t *testing.T) {
	localStreamAcceptedReplayAfterFreshChild(t, "fifo")
}

func localStreamAcceptedReplayAfterFreshChild(t *testing.T, protocol string) {
	t.Helper()
	binary := currentGatewayBinary(t)
	socket, c, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	if protocol == "fifo" {
		c = configureFIFOFixture(t, c)
	}
	start := func(signal syscall.Signal) (adapter.FramedStream, func()) {
		if protocol == "fifo" {
			peer, stop, _ := startFIFO(t, binary, c, signal, false)
			return peer, stop
		}
		conn, stop, _ := startParentFD(t, binary, c, protocol, signal, false)
		return conn, stop
	}
	conn, stop := start(syscall.SIGINT)
	call := func(id string, request core.Request, encrypted bool) *dispatch.DetachedResult {
		t.Helper()
		id += "-" + rand.Text()
		profile := "plain"
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: id, Profile: profile, Request: credentials.token(t, request, encrypted)})
		response := readFrame(t, conn)
		if response.CorrelationID != id || response.Error != nil || response.Result == nil {
			t.Fatal("local-stream call failed", response)
		}
		return response.Result
	}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "pair-restart-replay", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "same accepted intent after child restart 🙂\nwhole body", IssuedAt: &issued, ExpiresAt: &expires}
	admitted := call("initial", request, false).Result
	if admitted == nil || !admitted.Durable {
		t.Fatal("send was not durable", admitted)
	}
	var receipt *core.Result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		receipt = call("receipt", core.Request{Version: 1, RequestID: "pair-initial-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result
		if receipt != nil && receipt.State == core.Accepted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if receipt == nil || receipt.State != core.Accepted || receipt.SendAttempts != 1 {
		t.Fatal("send not accepted once", receipt)
	}
	event := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
		t.Fatal("native observer mismatch", event)
	}
	stop()
	identity := fixtureIdentity(t, c)
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	before, lookupErr := journal.LookupAuthor(t.Context(), identity, request.RequestID)
	closeErr := journal.Close()
	if lookupErr != nil || closeErr != nil || before.UpstreamID == "" || before.Result.State != core.Accepted || !before.Result.Durable || before.Result.MessageID != receipt.MessageID || before.Result.SendAttempts != 1 {
		t.Fatal("accepted intent missing", before, lookupErr, closeErr)
	}
	conn, stop = start(syscall.SIGTERM)
	replay := call("fresh-encrypted-replay", request, true).Result
	if replay == nil || !replay.Durable || replay.State != core.Accepted || replay.MessageID != receipt.MessageID || replay.SendAttempts != 1 {
		t.Fatal("fresh child changed acceptance", replay)
	}
	stop()
	journal, err = store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	after, lookupErr := journal.LookupAuthor(t.Context(), identity, request.RequestID)
	closeErr = journal.Close()
	if lookupErr != nil || closeErr != nil || after.UpstreamID != before.UpstreamID || after.Result.State != core.Accepted || !after.Result.Durable || after.Result.MessageID != receipt.MessageID || after.Result.SendAttempts != 1 {
		t.Fatal("fresh child changed durable upstream identity", after, lookupErr, closeErr)
	}
	native := nativeWebTransportMessages(t, socket, request.Target.Name)
	if len(native) != 1 || native[0].ID != receipt.MessageID || native[0].From != event.From || native[0].Target != event.Target || native[0].Message != event.Message {
		t.Fatal("restart replay changed native records", native)
	}
}
