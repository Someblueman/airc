//go:build darwin || linux

package service

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testtls"
)

func lifecycleConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "lifecycle", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStartupBindFailureClosesEarlierListenerAndJournal(t *testing.T) {
	c := lifecycleConfig(t)
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := first.Addr().String()
	_ = first.Close()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	c.Listeners = []config.Listener{
		{ID: "first", Protocol: "http", Address: address, Enabled: true, Operations: []core.Operation{core.Send}},
		{ID: "second", Protocol: "http", Address: occupied.Addr().String(), Enabled: true, Operations: []core.Operation{core.Send}},
	}
	var output bytes.Buffer
	if err := Run(context.Background(), c, &output); err == nil || output.Len() != 0 {
		t.Fatal("failed startup claimed ready", err, output.String())
	}
	if conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("earlier listener leaked")
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal("failed startup leaked journal ownership", err)
	}
	_ = s.Close()
}

func TestStartupInvalidCertificateDoesNotCreateJournal(t *testing.T) {
	c := lifecycleConfig(t)
	certificate := testtls.New(t)
	if err := os.WriteFile(certificate.KeyPath, []byte("invalid key must not appear in error"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Address: "127.0.0.1:0", Enabled: true, TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath}}
	var output bytes.Buffer
	if err := Run(context.Background(), c, &output); err == nil || output.Len() != 0 {
		t.Fatal("invalid TLS started service", err)
	}
	if _, err := os.Stat(filepath.Join(c.StateDir, "journal.sqlite")); !os.IsNotExist(err) {
		t.Fatal("invalid TLS mutated journal", err)
	}
}
