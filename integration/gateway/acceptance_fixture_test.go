package gateway_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func acceptanceJournalFixture(t *testing.T, socket, accountToken string) (config.Config, auth.Identity) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version = 1
	c.Realm = "wire-fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	accountFile := filepath.Join(root, "account.token")
	if err := os.WriteFile(accountFile, []byte(accountToken), 0600); err != nil {
		t.Fatal(err)
	}
	bearerFile := filepath.Join(root, "bearer.token")
	if err := auth.CreateCredential(bearerFile); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "fixture-backend", Network: "unix", Address: socket, Account: "fixture-author", AccountTokenFile: accountFile}}
	c.Principals = []config.Principal{{ID: "device", Binding: "fixture-backend", BearerTokenFile: bearerFile, Operations: []core.Operation{core.Send, core.Reply, core.ReceiptGet}, Targets: []core.Target{{Kind: "room", Name: "#gateway-fixture"}}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(bearerFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	return c, identity
}
