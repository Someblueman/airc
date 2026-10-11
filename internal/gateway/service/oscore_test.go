//go:build darwin || linux

package service

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func oscoreServiceConfig(t *testing.T) config.Config {
	t.Helper()
	binary := os.Getenv("AIRC_OSCORE_GATEWAY")
	if binary == "" {
		t.Skip("requires owned sanitizer-enabled native OSCORE worker")
	}
	c := upstreamServiceConfig(t)
	root := filepath.Dir(c.StateDir)
	c.Listeners = []config.Listener{{ID: "oscore", Protocol: "coap_oscore", Address: "127.0.0.1:0", Enabled: true, Operations: []core.Operation{core.Send}, OSCORE: &config.OSCORE{Binary: binary, ContextFile: filepath.Join(root, "context.json")}}}
	material, err := auth.CreateOSCOREContext(c.Listeners[0].OSCORE.ContextFile, auth.OSCOREOwner{Realm: c.Realm, Listener: "oscore"})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ProvisionOSCORE(t.Context(), store.OSCOREKey{Digest: digest, Listener: "oscore"}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOSCOREServiceReadyAndJoinedRestart(t *testing.T) {
	c := oscoreServiceConfig(t)
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		reader, writer := io.Pipe()
		done := make(chan error, 1)
		go func() { err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
		t.Cleanup(func() { cancel(); _ = reader.Close() })
		var ready struct {
			State     string `json:"state"`
			Listeners []struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			} `json:"listeners"`
		}
		if err := json.NewDecoder(reader).Decode(&ready); err != nil {
			t.Fatal(err)
		}
		if ready.State != "ready" || len(ready.Listeners) != 1 || ready.Listeners[0].ID != "oscore" {
			t.Fatal("wrong runtime readiness", ready)
		}
		address, err := net.ResolveUDPAddr("udp", ready.Listeners[0].Address)
		if err != nil || address.Port == 0 {
			t.Fatal("missing native endpoint", address, err)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("native service did not join")
		}
		_ = reader.Close()
		conn, err := net.ListenUDP("udp", address)
		if err != nil {
			t.Fatal("joined service retained native endpoint", err)
		}
		_ = conn.Close()
	}
}
