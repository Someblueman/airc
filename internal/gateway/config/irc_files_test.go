package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIRCCredentialReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	password, ca, token := filepath.Join(root, "password"), filepath.Join(root, "ca"), filepath.Join(root, "alice.token")
	for _, p := range []string{password, ca, token} {
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := Defaults()
	c.StateDir = filepath.Join(root, "state")
	c.IRCBridges = []IRCBridge{{PasswordFile: password, TLSCAFile: ca, Accounts: []BridgeAccount{{AccountTokenFile: token}}}}
	// One account token may be mapped on several bridges.
	c.XMPPBridges = []XMPPBridge{{Accounts: []BridgeAccount{{AccountTokenFile: token}}}}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(token, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err == nil {
		t.Fatal("public account token accepted")
	}
	if err := os.Chmod(token, 0600); err != nil {
		t.Fatal(err)
	}
	c.XMPPBridges = nil
	if err := os.Chmod(password, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err == nil {
		t.Fatal("public password accepted")
	}
	if err := os.Chmod(password, 0600); err != nil {
		t.Fatal(err)
	}
	c.IRCBridges[0].TLSCAFile = password
	if err := c.CheckFiles(); err == nil {
		t.Fatal("aliased password/trust reference accepted")
	}
	c.IRCBridges[0].TLSCAFile = filepath.Join(root, "missing")
	if err := c.CheckFiles(); err == nil {
		t.Fatal("missing trust reference accepted")
	}
}
