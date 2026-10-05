//go:build darwin || linux

package gateway_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func provisionUDPResponseCredentials(t *testing.T, c *config.Config) (ed25519.PublicKey, *ecdsa.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signingDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	signingPath := filepath.Join(filepath.Dir(c.StateDir), "udp-response-signing.pem")
	if err := os.WriteFile(signingPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: signingDER}), 0600); err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipientDER, err := x509.MarshalPKIXPublicKey(&recipient.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	recipientPath := filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem")
	if err := os.WriteFile(recipientPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: recipientDER}), 0644); err != nil {
		t.Fatal(err)
	}
	c.ResponseSigningKey = &config.ResponseSigningKey{ID: "udp-response", PrivateKeyFile: signingPath}
	return public, recipient
}
