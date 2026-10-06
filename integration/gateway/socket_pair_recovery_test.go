//go:build darwin || linux

package gateway_test

import (
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSocketPairPendingTerminationAndFreshChildRecovery(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGKILL, syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			f := newFileMailboxFixture(t)
			nativeAddress := f.config.Bindings[0].Address
			relay := holdFileMailboxUpstream(t, f)
			f.config.Bindings[0].Address = relay.listener.Addr().String()
			f.config.Listeners[0].Address = ""
			conn, stop, child := startParentFD(t, f.binary, f.config, "socketpair", 0, signal == syscall.SIGKILL)
			call := func(correlation string, request core.Request, encrypted bool) *dispatch.DetachedResult {
				t.Helper()
				profile := "signed"
				if encrypted {
					profile = "encrypted"
				}
				writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: profile, Envelope: f.credentials.token(t, request, encrypted)})
				response := readFrame(t, conn)
				if response.CorrelationID != correlation || response.Error != nil || response.Result == nil {
					t.Fatal("socket-pair recovery call", response)
				}
				return response.Result
			}
			issued, expires := time.Now().Unix(), time.Now().Unix()+120
			request := core.Request{Version: 1, RequestID: "pair-pending-recovery", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "pending socket-pair intent survives termination 🙂\nwhole body", IssuedAt: &issued, ExpiresAt: &expires}
			initial := call("pending", request, false).Result
			if initial == nil || !initial.Durable || initial.State == core.Accepted {
				t.Fatal("held send was not pending durable intent", initial)
			}
			select {
			case <-relay.accepted:
			case <-time.After(time.Second):
				t.Fatal("backend did not enter held connection")
			}
			if native := nativeWebTransportMessages(t, nativeAddress, request.Target.Name); len(native) != 0 {
				t.Fatal("held intent reached native backend", native)
			}
			before := time.Now()
			if err := child.Process.Signal(signal); err != nil {
				t.Fatal("child not live at signal", err)
			}
			stop()
			if time.Since(before) >= 3*time.Second {
				t.Fatal("termination exceeded join bound")
			}
			if signal == syscall.SIGKILL {
				status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != signal {
					t.Fatal("wrong hard-kill result", child.ProcessState)
				}
			}
			identity := fixtureIdentity(t, f.config)
			journal, err := store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal(err)
			}
			pending, lookupErr := journal.Lookup(t.Context(), identity, request.RequestID)
			closeErr := journal.Close()
			if lookupErr != nil || closeErr != nil || pending.UpstreamID == "" || !pending.Result.Durable || pending.Result.State == core.Accepted {
				t.Fatal("termination lost pending intent", pending, lookupErr, closeErr)
			}
			relay.forward.Store(true)
			conn, stop, _ = startParentFD(t, f.binary, f.config, "socketpair", syscall.SIGTERM, false)
			replay := call("fresh-encrypted", request, true).Result
			if replay == nil || !replay.Durable {
				t.Fatal("fresh child lost admission", replay)
			}
			var receipt *core.Result
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				receipt = call("receipt", core.Request{Version: 1, RequestID: "pair-recovered-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, true).Result
				if receipt != nil && receipt.State == core.Accepted {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if receipt == nil || receipt.State != core.Accepted {
				t.Fatal("recovery did not reach acceptance", receipt)
			}
			event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
			if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
				t.Fatal("recovery observer mismatch", event)
			}
			stop()
			journal, err = store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal(err)
			}
			recovered, lookupErr := journal.Lookup(t.Context(), identity, request.RequestID)
			closeErr = journal.Close()
			if lookupErr != nil || closeErr != nil || recovered.UpstreamID != pending.UpstreamID {
				t.Fatal("recovery changed upstream identity", recovered, lookupErr, closeErr)
			}
			native := nativeWebTransportMessages(t, nativeAddress, request.Target.Name)
			if len(native) != 1 || native[0].ID != receipt.MessageID || native[0].From != event.From || native[0].Target != event.Target || native[0].Message != event.Message {
				t.Fatal("recovery native record mismatch", native)
			}
		})
	}
}
