//go:build darwin || linux

package service

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestOSCOREServiceRejectsUnprovisionedOrWrongOwner(t *testing.T) {
	for _, wrongOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_state", true: "wrong_owner"}[wrongOwner], func(t *testing.T) {
			c := oscoreServiceConfig(t)
			path := filepath.Join(filepath.Dir(c.StateDir), "unprovisioned.json")
			owner := auth.OSCOREOwner{Realm: c.Realm, Listener: "oscore"}
			if wrongOwner {
				owner.Realm = "other"
			}
			if _, err := auth.CreateOSCOREContext(path, owner); err != nil {
				t.Fatal(err)
			}
			c.Listeners[0].OSCORE.ContextFile = path
			var output bytes.Buffer
			if err := Run(t.Context(), c, &output); err == nil || output.Len() != 0 {
				t.Fatal("invalid context claimed ready", err, output.String())
			}
			s, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("failed startup retained journal", err)
			}
			defer s.Close()
			material, err := auth.ReadOSCOREContext(path)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := material.Digest()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.LoadOSCORE(t.Context(), store.OSCOREKey{Digest: digest, Listener: "oscore"}); err == nil {
				t.Fatal("startup initialized missing state")
			}
		})
	}
}

func TestOSCOREServiceBindFailureJoinsEarlierChild(t *testing.T) {
	c := oscoreServiceConfig(t)
	available, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	address := available.LocalAddr().(*net.UDPAddr)
	_ = available.Close()
	c.Listeners[0].Address = address.String()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	c.Listeners = append(c.Listeners, config.Listener{ID: "occupied", Protocol: "http", Address: occupied.Addr().String(), Enabled: true, Operations: []core.Operation{core.Send}})
	var output bytes.Buffer
	if err := Run(t.Context(), c, &output); err == nil || output.Len() != 0 {
		t.Fatal("failed startup claimed ready", err)
	}
	rebound, err := net.ListenUDP("udp", address)
	if err != nil {
		t.Fatal("earlier native child retained port", err)
	}
	_ = rebound.Close()
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("earlier native child retained journal", err)
	}
	_ = s.Close()
	if err := os.Chmod(c.Listeners[0].OSCORE.ContextFile, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err == nil {
		t.Fatal("public OSCORE context allowed")
	}
}
