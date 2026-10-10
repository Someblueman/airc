//go:build darwin || linux

package oscore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func socketPair(t *testing.T) (net.Conn, *os.File) {
	t.Helper()
	parent, child, err := privateSocketPair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
	return parent, child
}

func runNativeProbe(t *testing.T, f *bridgeFixture, mode string, closeStore bool) string {
	t.Helper()
	binary := os.Getenv("AIRC_OSCORE_PERSISTENCE_PROBE")
	if binary == "" {
		t.Skip("requires the owned sanitizer-enabled native persistence probe")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("probe path must be absolute")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	parent, child := socketPair(t)
	control, childControl := socketPair(t)
	data, err := f.material.NativeConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(t.TempDir(), "peer.conf")
	err = os.WriteFile(peerPath, data, 0600)
	clear(data)
	if err != nil {
		t.Fatal(err)
	}
	peerFile, err := os.Open(peerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer peerFile.Close()
	cmd := exec.CommandContext(ctx, binary, mode)
	cmd.ExtraFiles = []*os.File{child, peerFile, childControl}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = child.Close()
	_ = childControl.Close()
	done := make(chan error, 1)
	go func() { done <- ServePersistence(ctx, parent, f.store, f.material, "fixture") }()
	waited := false
	joined := false
	defer func() {
		cancel()
		_ = control.Close()
		_ = parent.Close()
		_ = cmd.Process.Kill()
		if !waited {
			_ = cmd.Wait()
		}
		if !joined {
			<-done
		}
	}()
	if err := control.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(control, ready[:]); err != nil || ready[0] != 'R' {
		t.Fatal("native initialization failed", err)
	}
	if closeStore {
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := control.Write([]byte{'G'}); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("native probe %s: %v %s", mode, err, output.Bytes())
	}
	err = <-done
	joined = true
	if closeStore {
		if !errors.Is(err, store.ErrClosed) {
			t.Fatal("closed journal failure hidden", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestNativeOSCORESenderReservationsAndRestart(t *testing.T) {
	f := newBridgeFixture(t)
	if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"nonce=0", "nonce=8"} {
		if output := runNativeProbe(t, f, "sender-ok", false); !strings.Contains(output, want) {
			t.Fatal("native nonce did not resume", i)
		}
		state, err := f.store.LoadOSCORE(t.Context(), f.key)
		if err != nil || state.SenderEnd != uint64((i+1)*8) {
			t.Fatal("reservation not committed", state, err)
		}
	}
}

func TestNativeOSCOREReceiverPersistenceAndReplay(t *testing.T) {
	f := newBridgeFixture(t)
	if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
		t.Fatal(err)
	}
	// This control explicitly starts from restored sequence-zero state. It does
	// not claim initial Echo/bootstrap acceptance.
	if err := f.store.SaveOSCOREReceiver(t.Context(), f.key, 0, 1); err != nil {
		t.Fatal(err)
	}
	runNativeProbe(t, f, "receiver-tamper", false)
	state, err := f.store.LoadOSCORE(t.Context(), f.key)
	if err != nil || state.ReceiverSequence != 0 || state.ReceiverWindow != 1 {
		t.Fatal("unauthenticated persistence", state, err)
	}
	runNativeProbe(t, f, "receiver-ok", false)
	state, err = f.store.LoadOSCORE(t.Context(), f.key)
	if err != nil || state.ReceiverSequence != 8 || state.ReceiverWindow != 257 {
		t.Fatal("authenticated window not durable", state, err)
	}
	runNativeProbe(t, f, "receiver-replay", false)
}

func TestNativeOSCOREFreshEchoBootstrap(t *testing.T) {
	f := newBridgeFixture(t)
	if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
		t.Fatal(err)
	}
	runNativeProbe(t, f, "receiver-fresh", false)
	state, err := f.store.LoadOSCORE(t.Context(), f.key)
	if err != nil || !state.ReceiverInitialized || state.ReceiverSequence != 9 || state.ReceiverWindow != 1 || state.SenderEnd != 8 {
		t.Fatal("fresh Echo state not committed", state, err)
	}
}

func TestNativeOSCOREResourceBounds(t *testing.T) {
	f := newBridgeFixture(t)
	if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
		t.Fatal(err)
	}
	runNativeProbe(t, f, "receiver-bounds", false)
	state, err := f.store.LoadOSCORE(t.Context(), f.key)
	if err != nil || state != (store.OSCOREState{}) {
		t.Fatal("allocation control changed crypto state", state, err)
	}
}

func TestNativeOSCOREPersistenceFailurePoisonsCrypto(t *testing.T) {
	for _, mode := range []string{"sender-fail", "receiver-fail"} {
		t.Run(mode, func(t *testing.T) {
			f := newBridgeFixture(t)
			if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
				t.Fatal(err)
			}
			if err := f.store.SaveOSCOREReceiver(t.Context(), f.key, 0, 1); err != nil {
				t.Fatal(err)
			}
			runNativeProbe(t, f, mode, true)
			reopened, err := store.Open(t.Context(), f.configuration)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			state, err := reopened.LoadOSCORE(t.Context(), f.key)
			if err != nil || state.SenderEnd != 0 || state.ReceiverSequence != 0 || state.ReceiverWindow != 1 {
				t.Fatal("failed callback advanced state", state, err)
			}
		})
	}
}
