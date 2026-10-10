//go:build darwin || linux

package oscore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestNativeOSCOREOwnerPropagatesJournalFailure(t *testing.T) {
	f := newBridgeFixture(t)
	options := nativeOwnerOptions(t, f, "127.0.0.1:0")
	if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
		t.Fatal(err)
	}
	owner, err := Start(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	root := t.TempDir()
	peer, sequence, request := filepath.Join(root, "peer.conf"), filepath.Join(root, "peer.seq"), filepath.Join(root, "request.json")
	configuration, err := f.material.NativeConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(peer, configuration, 0600)
	clear(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{sequence: []byte("0\n"), request: []byte("{}")} {
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	client := os.Getenv("AIRC_OSCORE_COAP_CLIENT")
	if client == "" {
		client = filepath.Join(filepath.Dir(options.Binary), "libcoap", "coap-client")
	}
	client, err = ownedExecutable(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, client, "-v", "0", "-B", "5", "-m", "post", "-t", "50", "-f", request, "-E", peer+","+sequence, "coap://"+owner.Addr().String()+"/v1/coap/rpc")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = command.Wait() }()
	done := make(chan error, 1)
	go func() { done <- owner.Wait() }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNativeStopped) || !errors.Is(err, store.ErrClosed) {
			t.Fatal("journal failure hidden", err)
		}
	case <-ctx.Done():
		t.Fatal("journal failure did not join worker", ctx.Err())
	}
}
