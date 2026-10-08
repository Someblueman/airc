//go:build (darwin || linux) && arm64

package gateway_test

import (
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
	"testing"
	"time"
)

func TestSystemVQueueAcceptedReplayAfterKilledService(t *testing.T) {
	f := newSystemVFixtureShutdown(t, false)
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "sv-accepted-kill", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "retained accepted intent 世界 " + strings.Repeat("λ", 1400), IssuedAt: &issued, ExpiresAt: &expiry}
	first := systemVAccepted(t, f, request)
	if first.State != core.Accepted || first.SendAttempts != 1 || first.MessageID == "" {
		t.Fatal("initial acceptance", first)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == first.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatal("initial native mismatch", seen)
	}
	identity := f.source.Identity()
	if err := f.process.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("kill exited cleanly")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("killed gateway did not join")
	}
	p := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, p)
	if address := gatewayAddress(t, p); address != f.address {
		t.Fatal("recovery changed queue key", address)
	}
	recovered, err := sysvqueue.Open(identity.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Identity() != identity {
		t.Fatal("recovery changed lifecycle")
	}
	_ = f.source.Close()
	f.source = recovered
	replayed := systemVAccepted(t, f, request)
	if replayed.State != core.Accepted || replayed.SendAttempts != 1 || replayed.MessageID != first.MessageID {
		t.Fatal("accepted replay changed native attempt", replayed)
	}
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != first.MessageID || records[0].Message != request.Message {
		t.Fatal("accepted replay duplicated/lost intent", records)
	}
}
