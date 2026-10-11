//go:build darwin || linux

package gateway_test

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryMailboxPendingDeliveryKillAndSignalRecovery(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGKILL, syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			f := prepareDirectoryMailboxFixture(t)
			nativeAddress := f.config.Upstream.Address
			relay := holdFileMailboxUpstream(t, f.fileMailboxFixture)
			// Fix the endpoint before the first journal owner; restart keeps it.
			f.config.Upstream.Address = relay.listener.Addr().String()
			f.start(t)
			issued, expires := time.Now().Unix(), time.Now().Unix()+120
			request := core.Request{Version: 1, RequestID: "directory-pending-recovery", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "durable directory intent survives termination 🙂", IssuedAt: &issued, ExpiresAt: &expires}
			initial := directoryMailboxCall(t, f, request, false).Result.Result
			if initial == nil || !initial.Durable || initial.State == core.Accepted {
				t.Fatal("held directory delivery not durably pending", initial)
			}
			select {
			case <-relay.accepted:
			case <-time.After(time.Second):
				t.Fatal("backend never entered held connection")
			}
			if err := f.process.command.Process.Signal(signal); err != nil {
				t.Fatal("service no longer live at signal", err)
			}
			select {
			case <-f.process.done:
				if signal == syscall.SIGKILL {
					var exit *exec.ExitError
					if !errors.As(f.process.err, &exit) {
						t.Fatal("hard kill did not terminate service", f.process.err)
					}
					status, ok := exit.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != signal {
						t.Fatal("wrong hard-kill outcome", f.process.err)
					}
				} else if f.process.err != nil {
					t.Fatal("signal shutdown failed", f.process.err, f.process.stderr.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("directory service did not join after signal")
			}
			identity := fixtureIdentity(t, f.config)
			s, err := store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal("terminated service retained journal", err)
			}
			job, lookupErr := s.LookupAuthor(t.Context(), identity, request.RequestID)
			closeErr := s.Close()
			if lookupErr != nil || closeErr != nil || job.UpstreamID == "" || !job.Result.Durable || job.Result.State == core.Accepted {
				t.Fatal("termination lost pending durable intent", job, lookupErr, closeErr)
			}
			relay.forward.Store(true)
			f.start(t)
			replay := directoryMailboxCall(t, f, request, true).Result.Result
			if replay == nil || !replay.Durable {
				t.Fatal("replay lost durable admission", replay)
			}
			var receipt *core.Result
			deadline := time.Now().Add(3 * time.Second)
			for attempt := 0; ; attempt++ {
				receipt = directoryMailboxCall(t, f, core.Request{Version: 1, RequestID: "directory-recovered-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
				if receipt != nil && receipt.State == core.Accepted {
					break
				}
				if !time.Now().Before(deadline) {
					t.Fatal("replay did not reach native acceptance", receipt)
				}
			}
			f.stop(t)
			s, err = store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal(err)
			}
			recovered, lookupErr := s.LookupAuthor(t.Context(), identity, request.RequestID)
			closeErr = s.Close()
			if lookupErr != nil || closeErr != nil || recovered.UpstreamID != job.UpstreamID {
				t.Fatal("replay changed upstream ID", lookupErr, closeErr)
			}
			event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
			if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
				t.Fatal("recovered independent observer mismatch", event)
			}
			native := nativeWebTransportMessages(t, nativeAddress, request.Target.Name)
			if len(native) != 1 || native[0].ID != receipt.MessageID {
				t.Fatal("recovery lost or duplicated native record", native)
			}
		})
	}
}
