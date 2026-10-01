package main

import (
	"os"
	"path/filepath"
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
