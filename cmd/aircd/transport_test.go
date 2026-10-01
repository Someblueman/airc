package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestServerTransportValidatesKeysAndToken(t *testing.T) {
	cert := testtls.New(t)
	token := filepath.Join(t.TempDir(), "access.token")
	if err := admin.CreateToken(token); err != nil {
		t.Fatal(err)
	}
	cfg, access, err := serverTransport(cert.CertPath, cert.KeyPath, token, "")
	if err != nil || cfg == nil || !admin.ValidToken(access) {
		t.Fatal(cfg, err)
	}
	for _, paths := range [][4]string{{cert.CertPath, "", token, ""}, {cert.CertPath, cert.KeyPath, token, "sock"}, {"", "", cert.CertPath, ""}} {
		if _, _, err := serverTransport(paths[0], paths[1], paths[2], paths[3]); err == nil {
			t.Fatal("accepted invalid transport", paths)
		}
	}
	os.Chmod(cert.KeyPath, 0644)
	if _, _, err := serverTransport(cert.CertPath, cert.KeyPath, token, ""); err == nil {
		t.Fatal("accepted public key file")
	}
}

func TestDaemonLogRotationIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	writer, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	record := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 16; i++ {
		if _, err := writer.Write(record); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{path, path + ".1"} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > maxLogBytes || info.Mode().Perm() != 0600 {
			t.Fatal(info)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := openLog(alias); err == nil {
		t.Fatal("accepted symlink log")
	}
}
