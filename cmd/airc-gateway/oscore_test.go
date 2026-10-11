//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestOSCOREProvisioningCLI(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version = 1
	c.Realm = "fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	keyFile := filepath.Join(root, "oscore.json")
	if err := os.WriteFile(filepath.Join(root, "worker"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "oscore", Protocol: "coap_oscore", Address: "127.0.0.1:0", Operations: []core.Operation{core.Send}, OSCORE: &config.OSCORE{Binary: filepath.Join(root, "worker"), ContextFile: keyFile}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"oscore", "init", "--file", file, "--listener", "oscore", "--context-file", keyFile}
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatal(err)
	}
	material, err := auth.ReadOSCOREContext(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	native, err := material.NativeConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(native)
	if !strings.Contains(out.String(), "durable state created") || strings.Contains(out.String(), hex.EncodeToString(digest[:])) {
		t.Fatal("unsafe or missing provisioning output")
	}
	for _, line := range strings.Split(string(native), "\n") {
		parts := strings.SplitN(line, ",hex,\"", 2)
		if len(parts) == 2 && len(parts[1]) > 16 && strings.Contains(out.String(), strings.TrimSuffix(parts[1], "\"")) {
			t.Fatal("secret provisioning material printed")
		}
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	key := store.OSCOREKey{Digest: digest, Listener: "oscore"}
	state, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || state != (store.OSCOREState{}) {
		t.Fatal("matching fresh journal state missing", err)
	}
	if err := s.ReserveOSCORESender(t.Context(), key, 8); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run(args, &out); err == nil {
		t.Fatal("existing context reset")
	}
	s, err = store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.LoadOSCORE(t.Context(), key)
	if err != nil || state.SenderEnd != 8 {
		t.Fatal("failed reprovision reset state", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	unknown := append([]string(nil), args...)
	unknown[5], unknown[len(unknown)-1] = "other", filepath.Join(root, "other.json")
	if err := run(unknown, &out); err == nil {
		t.Fatal("context provisioned for an unknown listener")
	}
	if _, err := os.Stat(unknown[len(unknown)-1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused provisioning wrote a context file", err)
	}
}
