//go:build darwin || linux

package oscore

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestPersistenceRefusesMissingStateAndRealmBeforeMaterial(t *testing.T) {
	for _, realm := range []string{"fixture", "wrong"} {
		t.Run(realm, func(t *testing.T) {
			f := newBridgeFixture(t)
			owner, peer := net.Pipe()
			defer peer.Close()
			done := make(chan error, 1)
			go func() { done <- ServePersistence(t.Context(), owner, f.store, f.material, realm) }()
			joined := false
			defer func() {
				_ = peer.Close()
				if !joined {
					<-done
				}
			}()
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var byte [1]byte
			if n, err := peer.Read(byte[:]); n != 0 || !errors.Is(err, io.EOF) {
				t.Fatal("startup released material", n, err)
			}
			want := store.ErrNotFound
			if realm == "wrong" {
				want = auth.ErrCredential
			}
			err := <-done
			joined = true
			if !errors.Is(err, want) {
				t.Fatal("startup error", err)
			}
		})
	}
}

func TestPersistenceRejectsMalformedCallbacksAndJoinsOnCancel(t *testing.T) {
	for _, mode := range []string{"unknown", "sender-window", "truncated", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newBridgeFixture(t)
			if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			owner, peer := net.Pipe()
			defer peer.Close()
			done := make(chan error, 1)
			go func() { done <- ServePersistence(ctx, owner, f.store, f.material, "fixture") }()
			joined := false
			defer func() {
				cancel()
				_ = peer.Close()
				if !joined {
					<-done
				}
			}()
			if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var header [34]byte
			if _, err := io.ReadFull(peer, header[:]); err != nil {
				t.Fatal(err)
			}
			length := binary.BigEndian.Uint32(header[5:9])
			if length > 1024 {
				t.Fatal("unbounded initialization")
			}
			if _, err := io.CopyN(io.Discard, peer, int64(length)); err != nil {
				t.Fatal(err)
			}
			var callback [17]byte
			callback[0] = 3
			binary.BigEndian.PutUint64(callback[1:9], 8)
			if mode == "sender-window" {
				callback[0] = 1
				callback[16] = 1
			}
			switch mode {
			case "cancel":
				cancel()
			case "truncated":
				if _, err := peer.Write(callback[:2]); err != nil {
					t.Fatal(err)
				}
				_ = peer.Close()
			default:
				if _, err := peer.Write(callback[:]); err != nil {
					t.Fatal(err)
				}
				var ack [1]byte
				if _, err := io.ReadFull(peer, ack[:]); err != nil || ack[0] != 0 {
					t.Fatal("malformed callback acknowledged", ack, err)
				}
			}
			select {
			case err := <-done:
				joined = true
				if err == nil {
					t.Fatal("invalid callback accepted")
				}
				if (mode == "unknown" || mode == "sender-window") && !errors.Is(err, ErrProtocol) {
					t.Fatal("protocol error hidden", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("callback owner failed to join")
			}
			state, err := f.store.LoadOSCORE(t.Context(), f.key)
			if err != nil || state != (store.OSCOREState{}) {
				t.Fatal("invalid callback changed state", state, err)
			}
		})
	}
}
