//go:build darwin || linux

package oscore

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type bridgeFixture struct {
	configuration config.Config
	store         *store.Store
	material      *auth.OSCOREContext
	key           store.OSCOREKey
	signing       ed25519.PrivateKey
}

func newBridgeFixture(t *testing.T) *bridgeFixture {
	return configuredBridgeFixture(t, false)
}

func configuredBridgeFixture(t *testing.T, signed bool) *bridgeFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	account, principal := filepath.Join(root, "account"), filepath.Join(root, "device")
	for _, path := range []string{account, principal} {
		if err := auth.CreateCredential(path); err != nil {
			t.Fatal(err)
		}
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: account}}
	c.Principals = []config.Principal{{ID: "device", Binding: "backend", BearerTokenFile: principal, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}}}
	var signing ed25519.PrivateKey
	if signed {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signing = private
		data, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "signing.json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		c.Principals[0].SigningKeys = []config.SigningKey{{ID: "native-signer", PublicKeyFile: path}}
		c.Principals[0].Operations = append(c.Principals[0].Operations, core.ReceiptGet)
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	material, err := auth.CreateOSCOREContext(filepath.Join(root, "oscore.json"), auth.OSCOREOwner{Realm: c.Realm, Principal: "device", Binding: "backend"})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	key := store.OSCOREKey{Digest: digest, Principal: "device", Binding: "backend"}
	return &bridgeFixture{configuration: c, store: s, material: material, key: key, signing: signing}
}
