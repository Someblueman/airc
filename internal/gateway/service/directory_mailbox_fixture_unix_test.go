//go:build darwin || linux

package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	jose "github.com/go-jose/go-jose/v4"
)

func directoryServiceConfig(t *testing.T) config.Config {
	t.Helper()
	c := lifecycleConfig(t)
	root, err := filepath.EvalSymlinks(filepath.Dir(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	account, signer, queue := filepath.Join(root, "account"), filepath.Join(root, "signer.json"), filepath.Join(root, "queue")
	if err := auth.CreateCredential(account); err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := json.Marshal(jose.JSONWebKey{Key: public, KeyID: "signer", Algorithm: string(jose.EdDSA), Use: "sig"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signer, key, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{queue, filepath.Join(queue, "inbox"), filepath.Join(queue, "processing"), filepath.Join(queue, "results")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: account}}
	c.Principals = []config.Principal{{ID: "device", Binding: "backend", SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: signer}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}}}
	c.Listeners = []config.Listener{{ID: "queue", Protocol: "directory_mailbox", Address: queue, Enabled: true, Operations: []core.Operation{core.Send}, DirectoryMailbox: &config.DirectoryMailbox{Principal: "device"}}}
	return c
}
