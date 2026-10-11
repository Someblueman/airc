//go:build darwin || linux

package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
	jose "github.com/go-jose/go-jose/v4"
)

// signedServiceConfig builds a listener-free service fixture with a bearer
// credential, principal signing key and response signing key.
func signedServiceConfig(t *testing.T) (config.Config, testtls.Certificate, jose.Signer, ed25519.PublicKey) {
	t.Helper()
	c := directoryServiceConfig(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.ReceiptGet)
	root := filepath.Dir(c.StateDir)
	c.Principals[0].BearerTokenFile = filepath.Join(root, "bearer")
	if err := auth.CreateCredential(c.Principals[0].BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	signingPath := filepath.Join(root, "response.pem")
	if err := os.WriteFile(signingPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	c.ResponseSigningKey = &config.ResponseSigningKey{ID: "response", PrivateKeyFile: signingPath}
	cert := testtls.New(t)
	c.Limits.Sessions, c.Limits.SessionsPerPrincipal = 1, 1
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 0, 0
	c.Limits.Workers = 1
	c.Listeners = []config.Listener{}
	public, err := json.Marshal(jose.JSONWebKey{Key: pub})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Principals[0].SigningKeys[0].PublicKeyFile, public, 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "signer"))
	if err != nil {
		t.Fatal(err)
	}
	return c, cert, signer, pub
}
