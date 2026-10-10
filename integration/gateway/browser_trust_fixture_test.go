//go:build darwin || linux

package gateway_test

import (
	"context"
	cryptotls "crypto/tls"
	"crypto/x509"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/testtls"
)

// trustedBrowserCertificate issues only a leaf from an existing mkcert root.
// It never creates or installs a root, and the browser must validate the
// ordinary certificate chain itself.
func trustedBrowserCertificate(t *testing.T) testtls.Certificate {
	t.Helper()
	if _, err := exec.LookPath("mkcert"); err != nil {
		t.Fatal("mkcert is required for the trusted browser fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	rootOutput, err := exec.CommandContext(ctx, "mkcert", "-CAROOT").Output()
	if err != nil {
		t.Fatalf("locate existing mkcert root: %v", err)
	}
	caPath := filepath.Join(strings.TrimSpace(string(rootOutput)), "rootCA.pem")
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("existing mkcert root is required; refusing to let mkcert create one: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("invalid existing mkcert root certificate")
	}
	if runtime.GOOS == "darwin" {
		trust := exec.CommandContext(ctx, "security", "verify-cert", "-c", caPath, "-p", "ssl")
		if output, err := trust.CombinedOutput(); err != nil {
			t.Fatalf("existing mkcert root is not trusted for SSL; refusing to issue: %v: %s", err, output)
		}
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatalf("restrict fixture certificate directory: %v", err)
	}
	certPath, keyPath := filepath.Join(dir, "server.pem"), filepath.Join(dir, "server-key.pem")
	command := exec.CommandContext(ctx, "mkcert", "-cert-file", certPath, "-key-file", keyPath, "localhost", "127.0.0.1", "::1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("issue leaf with existing mkcert root: %v: %s", err, output)
	}
	if err := os.Chmod(keyPath, 0600); err != nil {
		t.Fatalf("restrict fixture private key: %v", err)
	}
	if _, err := auth.ServerCertificate(certPath, keyPath); err != nil {
		t.Fatalf("validate browser fixture certificate and key: %v", err)
	}
	pair, err := cryptotls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return testtls.Certificate{
		CertPath: certPath, KeyPath: keyPath, CAPath: caPath,
		Server: &cryptotls.Config{MinVersion: cryptotls.VersionTLS13, Certificates: []cryptotls.Certificate{pair}},
		Client: &cryptotls.Config{MinVersion: cryptotls.VersionTLS13, RootCAs: roots},
	}
}
