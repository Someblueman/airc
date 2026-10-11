//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package gateway_test

import (
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSystemVQueuePendingDeliveryAfterKilledService(t *testing.T) {
	var relay *fileMailboxRelay
	var nativeAddress string
	f := newSystemVFixtureBinding(t, false, func(c *config.Config) {
		nativeAddress = c.Upstream.Address
		relay = holdFileMailboxUpstream(t, &fileMailboxFixture{config: *c})
		c.Upstream.Address = relay.listener.Addr().String()
	})
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "sv-pending-kill", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "pending queue intent 世界 🙂", IssuedAt: &issued, ExpiresAt: &expiry}
	initial := systemVCall(t, f, request).Result.Result
	if initial == nil || !initial.Durable || initial.State == core.Accepted {
		t.Fatal("held delivery not durable pending", initial)
	}
	select {
	case <-relay.accepted:
	case <-time.After(time.Second):
		t.Fatal("native attempt never entered held connection")
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
		t.Fatal("killed service did not join")
	}
	submission := fixtureIdentity(t, f.config)
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	job, lookupErr := s.LookupAuthor(t.Context(), submission, request.RequestID)
	closeErr := s.Close()
	if lookupErr != nil || closeErr != nil || job.UpstreamID == "" || !job.Result.Durable || job.Result.State == core.Accepted {
		t.Fatal("pending intent lost", job, lookupErr, closeErr)
	}
	relay.forward.Store(true)
	p := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, p)
	if address := gatewayAddress(t, p); address != f.address {
		t.Fatal("recovery changed key", address)
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
	receipt := systemVAccepted(t, f, request)
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatal("native recovery mismatch", seen)
	}
	records := nativeWebTransportMessages(t, nativeAddress, request.Target.Name)
	if len(records) != 1 || records[0].ID != receipt.MessageID || records[0].Message != request.Message {
		t.Fatal("recovery duplicated/lost native intent", records)
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal(p.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovered gateway did not join")
	}
	s, err = store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	final, lookupErr := s.LookupAuthor(t.Context(), submission, request.RequestID)
	closeErr = s.Close()
	if lookupErr != nil || closeErr != nil || final.UpstreamID != job.UpstreamID {
		t.Fatal("recovery changed upstream identity", lookupErr, closeErr)
	}
}
