//go:build darwin || linux

package gateway_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBPv7PendingTerminationAndOriginalValidityRecovery(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGKILL, syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			f := newBPFixture(t)
			nativeAddress := f.config.Upstream.Address
			relay := holdFileMailboxUpstream(t, f)
			f.config.Upstream.Address = relay.listener.Addr().String()
			bpSaveConfig(t, f)
			now := time.Now().Unix()
			producer := bpIntent(t, f, 99, now-120, now+300, "BPv7 durable intent survives interruption 🙂")
			data := producer.wire()
			input := filepath.Join(f.directory, "pending.bpv7")
			if err := os.WriteFile(input, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, f.binary, "import-bpv7", "--file", f.path, "--input", input)
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
			if native := nativeWebTransportMessages(t, nativeAddress, "#gateway-fixture"); len(native) != 0 {
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
			pending, lookupErr := s.LookupAuthor(t.Context(), identity, producer.id())
			closeErr := s.Close()
			if lookupErr != nil || closeErr != nil || pending.Result.ExpiresAt != now+300 || !pending.Result.Durable || pending.UpstreamID == "" || pending.Result.State == core.Accepted {
				t.Fatal("lost durable pending intent", pending, lookupErr, closeErr)
			}
			relay.forward.Store(true)
			recovered := bpCall(t, f, data, false)
			if len(recovered.Entries) != 1 || !recovered.Entries[0].ReplayRejected || recovered.Entries[0].Imported || recovered.Entries[0].Result.State != core.Accepted {
				t.Fatal("pending replay did not recover original work", recovered)
			}
			s, err = store.Open(t.Context(), f.config)
			if err != nil {
				t.Fatal(err)
			}
			after, lookupErr := s.LookupAuthor(t.Context(), identity, producer.id())
			closeErr = s.Close()
			if lookupErr != nil || closeErr != nil || after.Result.ExpiresAt != now+300 || after.UpstreamID != pending.UpstreamID || after.Result.MessageID != recovered.Entries[0].Result.MessageID {
				t.Fatal("recovery changed durable identity", after, lookupErr, closeErr)
			}
			if native := nativeWebTransportMessages(t, nativeAddress, "#gateway-fixture"); len(native) != 1 || native[0].From != "fixture-author" || native[0].Message != "BPv7 durable intent survives interruption 🙂" {
				t.Fatal("wrong whole native recovery", native)
			}
		})
	}
}
