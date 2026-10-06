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
	account := filepath.Join(root, "account")
	principal := filepath.Join(root, "device")
	for _, path := range []string{account, principal} {
		if err := auth.CreateCredential(path); err != nil {
			t.Fatal(err)
		}
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: account}}
	c.Principals = []config.Principal{{ID: "device", Binding: "backend", BearerTokenFile: principal, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "oscore.json")
	args := []string{"oscore", "init", "--file", file, "--principal", "device", "--context-file", keyFile}
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
	key := store.OSCOREKey{Digest: digest, Principal: "device", Binding: "backend"}
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
	if _, err := s.Revoke(t.Context(), "device"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	revokedFile := filepath.Join(root, "revoked.json")
	args[len(args)-1] = revokedFile
	if err := run(args, &out); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("durable revocation bypassed", err)
	}
	orphan, err := auth.ReadOSCOREContext(revokedFile)
	if err != nil {
		t.Fatal("failed state creation did not retain diagnosable context", err)
	}
	orphanDigest, err := orphan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.LoadOSCORE(t.Context(), store.OSCOREKey{Digest: orphanDigest, Principal: "device", Binding: "backend"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("revoked context obtained state", err)
	}
}
