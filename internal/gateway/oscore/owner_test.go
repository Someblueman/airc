//go:build darwin || linux

package oscore

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func nativeOwnerOptions(t *testing.T, f *bridgeFixture, address string) Options {
	t.Helper()
	binary := os.Getenv("AIRC_OSCORE_GATEWAY")
	if binary == "" {
		t.Skip("requires the owned sanitizer-enabled native OSCORE worker")
	}
	return Options{Binary: binary, Address: address, Realm: "fixture", Store: f.store, Material: f.material, Dispatcher: new(dispatch.Dispatcher), Requests: make(chan struct{}, 1), Plain: &dispatch.Ingress{Source: "listener:oscore", Session: dispatch.NewSession()}}
}

func TestNativeOSCOREOwnerStartsAndJoins(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			f := newBridgeFixture(t)
			options := nativeOwnerOptions(t, f, address)
			if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
				t.Fatal(err)
			}
			owner, err := Start(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = owner.Close() })
			if owner.Addr().(*net.UDPAddr).Port == 0 {
				t.Fatal("missing bound port")
			}
			if _, err := Start(t.Context(), options); !errors.Is(err, store.ErrConflict) {
				t.Fatal("simultaneous native context allowed", err)
			}
			started := time.Now()
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if time.Since(started) > 4*time.Second {
				t.Fatal("native shutdown did not join promptly")
			}
			listener, err := net.ListenUDP("udp", owner.Addr().(*net.UDPAddr))
			if err != nil {
				t.Fatal("native listener retained after join", err)
			}
			_ = listener.Close()
			restarted, err := Start(t.Context(), options)
			if err != nil {
				t.Fatal("context lease not released", err)
			}
			if err := restarted.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeOSCOREOwnerUnexpectedExitAndParentCancellation(t *testing.T) {
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
	if err := owner.process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Wait(); !errors.Is(err, ErrNativeStopped) {
		t.Fatal("unexpected child death hidden", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	restarted, err := Start(ctx, options)
	if err != nil {
		t.Fatal("dead child's context lease retained", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	cancel()
	if err := restarted.Wait(); err != nil {
		t.Fatal("parent cancellation was not joined", err)
	}
}

func TestNativeOSCOREOwnerRefusesMissingStateAndOccupiedPort(t *testing.T) {
	f := newBridgeFixture(t)
	options := nativeOwnerOptions(t, f, "127.0.0.1:0")
	if _, err := Start(t.Context(), options); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("missing state initialized", err)
	}
	if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
		t.Fatal(err)
	}
	occupied, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	options.Address = occupied.LocalAddr().String()
	if _, err := Start(t.Context(), options); err == nil {
		t.Fatal("occupied native port accepted")
	}
	options.Address = "127.0.0.1:0"
	owner, err := Start(t.Context(), options)
	if err != nil {
		t.Fatal("failed startup retained context", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.LoadOSCORE(t.Context(), f.key)
	if err != nil || state != (store.OSCOREState{}) {
		t.Fatal("idle lifecycle changed crypto state", state, err)
	}
}
