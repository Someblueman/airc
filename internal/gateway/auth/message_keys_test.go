//go:build unix

package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	jose "github.com/go-jose/go-jose/v4"
)

func TestMessageKeyFileBoundaries(t *testing.T) {
	c, _, _ := messageFixture(t)
	private := c.RecipientKeys[0].PrivateKeyFile
	material, _ := os.ReadFile(private)
	if err := os.WriteFile(private, append([]byte("ignored prefix\n"), material...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c); err == nil {
		t.Fatal("extra PEM text accepted")
	}
	if err := os.WriteFile(private, material, 0600); err != nil {
		t.Fatal(err)
	}
	public := c.Principals[0].SigningKeys[0].PublicKeyFile
	if err := os.Chmod(public, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c); err == nil {
		t.Fatal("writable public trust file accepted")
	}
	if err := os.Chmod(public, 0644); err != nil {
		t.Fatal(err)
	}
	other := c.Principals[0]
	other.ID = "other"
	data, _ := os.ReadFile(public)
	duplicate := filepath.Join(filepath.Dir(public), "duplicate.json")
	if err := os.WriteFile(duplicate, data, 0644); err != nil {
		t.Fatal(err)
	}
	other.SigningKeys = []config.SigningKey{{ID: "other", PublicKeyFile: duplicate}}
	c.Principals = append(c.Principals, other)
	if _, err := New(c); err == nil {
		t.Fatal("same key mapped to different principals")
	}
	c.Principals = c.Principals[:1]
	wrong, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(wrong)
	if err := os.WriteFile(c.RecipientKeys[0].PrivateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c); err == nil {
		t.Fatal("unapproved recipient curve accepted")
	}
}

// Published RFC 8037 Appendix A.4/A.5 vector, independently of gateway schema.
func TestRFC8037Ed25519Vector(t *testing.T) {
	pub, _ := base64.RawURLEncoding.DecodeString("11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo")
	token := "eyJhbGciOiJFZERTQSJ9.RXhhbXBsZSBvZiBFZDI1NTE5IHNpZ25pbmc.hgyY0il_MGCjP0JzlnLWG1PPOt7-09PGcvMg3AIbQR6dWbhijcNR4ki4iylGjg5BhVsPt9g7sVvpAr_MuM0KAg"
	o, err := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := o.Verify(ed25519.PublicKey(pub))
	if err != nil || string(payload) != "Example of Ed25519 signing" {
		t.Fatalf("RFC vector: %v", err)
	}
}
