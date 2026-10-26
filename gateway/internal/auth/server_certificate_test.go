//go:build unix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/testtls"
)

func TestServerCertificateNativeOwnershipAndBounds(t *testing.T) {
	c := testtls.New(t)
	if _, err := ServerCertificate(c.CertPath, c.KeyPath); err != nil {
		t.Fatal("valid certificate rejected", err)
	}
	if err := os.Chmod(c.KeyPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCertificate(c.CertPath, c.KeyPath); !errors.Is(err, ErrCredential) {
		t.Fatal("readable private key accepted", err)
	}
	if err := os.Chmod(c.KeyPath, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(c.KeyPath), "link.pem")
	if err := os.Symlink(c.KeyPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCertificate(c.CertPath, link); !errors.Is(err, ErrCredential) {
		t.Fatal("symlink key accepted", err)
	}
	if err := os.WriteFile(c.KeyPath, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCertificate(c.CertPath, c.KeyPath); !errors.Is(err, ErrCredential) {
		t.Fatal("oversized key accepted", err)
	}
}
