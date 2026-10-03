package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestServiceInstallRejectsUnsafeRemoteAndKeepsData(t *testing.T) {
	agentEnv(t)
	t.Setenv("HOME", t.TempDir())
	cert := testtls.New(t)
	binary := filepath.Join(t.TempDir(), "aircd")
	if err := os.WriteFile(binary, []byte("test"), 0700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	access := filepath.Join(t.TempDir(), "access.token")
	if err := admin.CreateToken(access); err != nil {
		t.Fatal(err)
	}
	base := serviceInstallOptions{binary: binary, listen: "0.0.0.0:6697", history: 100, connections: 32, messageSize: 4096}
	if _, err := base.config("test", dir); err == nil {
		t.Fatal("accepted unprotected remote service")
	}
	if _, err := os.Stat(filepath.Join(dir, "admin.token")); !os.IsNotExist(err) {
		t.Fatal("invalid config wrote credential")
	}
	base.cert, base.key, base.access = cert.CertPath, cert.KeyPath, access
	c, err := base.config("test", dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Args[0] != "--listen" || c.Args[1] != "0.0.0.0:6697" {
		t.Fatal(c)
	}
	operator, err := admin.ReadToken(filepath.Join(dir, "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	token, _ := admin.ReadToken(access)
	if operator == token {
		t.Fatal("credentials share roles")
	}
	base.access = filepath.Join(dir, "admin.token")
	if _, err := base.config("test", dir); err == nil {
		t.Fatal("allowed operator token for room access")
	}
}

func syncArgs(args []string) []string {
	var found []string
	for i, arg := range args {
		if arg == "--sync" && i+1 < len(args) {
			found = append(found, args[i+1])
		}
	}
	return found
}

func TestServiceInstallSyncOption(t *testing.T) {
	agentEnv(t)
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "aircd")
	if err := os.WriteFile(binary, []byte("test"), 0700); err != nil {
		t.Fatal(err)
	}
	var install serviceInstallOptions
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	install.flags(fs)
	if err := fs.Parse([]string{"--binary", binary}); err != nil {
		t.Fatal(err)
	}
	if install.sync != "full" {
		t.Fatalf("default sync %q", install.sync)
	}
	// The default is left to the daemon, so the service follows it.
	c, err := install.config("test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := syncArgs(c.Args); len(got) != 0 {
		t.Fatalf("default mode was passed explicitly: %v", c.Args)
	}
	for _, mode := range []string{"fsync", "none"} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		var install serviceInstallOptions
		install.flags(fs)
		if err := fs.Parse([]string{"--binary", binary, "--sync", mode}); err != nil {
			t.Fatal(err)
		}
		c, err := install.config("test", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if got := syncArgs(c.Args); len(got) != 1 || got[0] != mode {
			t.Fatalf("%s: args %v", mode, c.Args)
		}
	}
	dir := t.TempDir()
	install.sync = "sometimes"
	if _, err := install.config("test", dir); err == nil || !strings.Contains(err.Error(), "--sync") {
		t.Fatalf("accepted invalid sync mode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "admin.token")); !os.IsNotExist(err) {
		t.Fatal("invalid sync mode wrote a credential")
	}
}
