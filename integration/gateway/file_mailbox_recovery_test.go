//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestFileMailboxPendingDeliveryKillAndSignalRecovery(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGKILL, syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			f := newFileMailboxFixture(t)
			nativeAddress := f.config.Upstream.Address
			relay := holdFileMailboxUpstream(t, f)
			f.config.Upstream.Address = relay.listener.Addr().String()
			data, err := json.Marshal(f.config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.path, data, 0600); err != nil {
				t.Fatal(err)
			}
			issued, expires := time.Now().Unix(), time.Now().Unix()+120
			request := core.Request{Version: 1, RequestID: "file-pending-recovery", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "durable file intent survives process termination 🙂", IssuedAt: &issued, ExpiresAt: &expires}
			data, err = json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, false)})
			if err != nil {
				t.Fatal(err)
			}
			fileMailboxPublish(t, f, data)
			command := fileMailboxCommand(t, f)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			joined := false
			t.Cleanup(func() {
				if !joined {
					_ = command.Process.Kill()
					<-done
				}
			})
			output := filepath.Join(f.directory, "response.json")
			deadline := time.Now().Add(3 * time.Second)
			var initial adapter.FrameResponse
			for {
				data, err = os.ReadFile(output)
				if err == nil && json.Unmarshal(data, &initial) == nil {
					break
				}
				if !time.Now().Before(deadline) {
					t.Fatal("pending intake response was not published")
				}
				select {
				case err := <-done:
					joined = true
					t.Fatal("CLI exited before interruption", err)
				default:
				}
				time.Sleep(5 * time.Millisecond)
			}
			if initial.Error != nil || initial.Result == nil || initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.State == core.Accepted {
				t.Fatal("held delivery not durably pending", initial)
			}
			select {
			case <-relay.accepted:
			case <-time.After(time.Second):
				t.Fatal("backend never entered held connection")
			}
			before := time.Now()
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal("CLI no longer live at signal", err)
			}
			select {
			case err := <-done:
				joined = true
				if signal == syscall.SIGKILL {
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatal("hard kill did not terminate child", err)
					}
					status, ok := exit.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != signal {
						t.Fatal("wrong hard-kill outcome", err)
					}
				} else if err != nil {
					t.Fatal("signal shutdown failed", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("mailbox child did not join after signal")
			}
			if time.Since(before) >= 3*time.Second {
				t.Fatal("shutdown exceeded interruption bound")
			}
			identity := fixtureIdentity(t, f.config)
			s, err := store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal("terminated child retained journal ownership", err)
			}
			job, lookupErr := s.LookupAuthor(t.Context(), identity, request.RequestID)
			closeErr := s.Close()
			if lookupErr != nil || closeErr != nil || job.UpstreamID == "" || !job.Result.Durable || job.Result.State == core.Accepted {
				t.Fatal("termination lost pending durable intent", job, lookupErr, closeErr)
			}
			if err := os.Remove(output); err != nil {
				t.Fatal(err)
			}
			relay.forward.Store(true)
			replay := fileMailboxCall(t, f, request, true).Result.Result
			if replay == nil || !replay.Durable {
				t.Fatal("recovery did not retain admission", replay)
			}
			receipt := fileMailboxCall(t, f, core.Request{Version: 1, RequestID: "file-recovered-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
			if receipt == nil || receipt.State != core.Accepted {
				t.Fatal("recovery did not reach native acceptance", receipt)
			}
			s, err = store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal(err)
			}
			recovered, lookupErr := s.LookupAuthor(t.Context(), identity, request.RequestID)
			closeErr = s.Close()
			if lookupErr != nil || closeErr != nil || recovered.UpstreamID != job.UpstreamID {
				t.Fatal("recovery changed stable upstream ID", lookupErr, closeErr)
			}
			event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
			if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
				t.Fatal("recovered independent observer mismatch", event)
			}
			native := nativeWebTransportMessages(t, nativeAddress, request.Target.Name)
			if len(native) != 1 || native[0].ID != receipt.MessageID {
				t.Fatal("recovery duplicated or lost native record", native)
			}
		})
	}
}
