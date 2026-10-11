//go:build darwin || linux

package gateway_test

import (
	"context"
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
)

func TestOfflineBundlePendingTerminationAndFreshImportRecovery(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGKILL, syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			f := newOfflineBundleFixture(t)
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
			request := core.Request{Version: 1, RequestID: "offline-pending", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "offline durable intent survives interruption 🙂"}
			token := f.credentials.token(t, request, false)
			data, err = json.Marshal(adapter.OfflineBundle{Version: 1, Requests: rawRequests([]string{token})})
			if err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(f.directory, "pending.json")
			if err := os.WriteFile(input, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, f.binary, "import-bundle", "--file", f.path, "--input", input)
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
			select {
			case <-relay.accepted:
			case <-time.After(3 * time.Second):
				t.Fatal("durably admitted job did not reach held backend")
			}
			if native := nativeWebTransportMessages(t, nativeAddress, request.Target.Name); len(native) != 0 {
				t.Fatal("held job already delivered", native)
			}
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				joined = true
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal("interruption did not produce failure", err)
				}
				status, ok := exit.Sys().(syscall.WaitStatus)
				if !ok || signal == syscall.SIGKILL && (!status.Signaled() || status.Signal() != signal) || signal != syscall.SIGKILL && (!status.Exited() || status.ExitStatus() != 1) {
					t.Fatal("wrong interruption outcome", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("offline child did not join after interruption")
			}
			identity := fixtureIdentity(t, f.config)
			s, err := store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal("interrupted CLI retained journal", err)
			}
			pending, lookupErr := s.LookupAuthor(t.Context(), identity, request.RequestID)
			closeErr := s.Close()
			if lookupErr != nil || closeErr != nil || !pending.Result.Durable || pending.UpstreamID == "" || pending.Result.State == core.Accepted {
				t.Fatal("lost durable pending intent", pending, lookupErr, closeErr)
			}
			relay.forward.Store(true)
			recovered := offlineBundleCall(t, f, []string{token}, false)
			if len(recovered.Entries) != 1 || !recovered.Entries[0].ReplayRejected || recovered.Entries[0].Imported || recovered.Entries[0].Result.State != core.Accepted {
				t.Fatal("pending replay did not recover original work", recovered)
			}
			s, err = store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal(err)
			}
			after, lookupErr := s.LookupAuthor(t.Context(), identity, request.RequestID)
			closeErr = s.Close()
			if lookupErr != nil || closeErr != nil || after.UpstreamID != pending.UpstreamID || after.Result.MessageID != recovered.Entries[0].Result.MessageID {
				t.Fatal("recovery changed durable identity", after, lookupErr, closeErr)
			}
			if native := nativeWebTransportMessages(t, nativeAddress, request.Target.Name); len(native) != 1 || native[0].From != "fixture-author" || native[0].Message != request.Message {
				t.Fatal("wrong whole native recovery", native)
			}
		})
	}
}
