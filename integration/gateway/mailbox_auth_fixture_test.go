//go:build darwin || linux

package gateway_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	jose "github.com/go-jose/go-jose/v4"
)

func mailboxOtherSigner(t *testing.T, f *fileMailboxFixture) *detachedCredentials {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := json.Marshal(jose.JSONWebKey{Key: public, Algorithm: string(jose.EdDSA), Use: "sig"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(f.config.StateDir), "other-signing.json")
	if err := os.WriteFile(path, key, 0644); err != nil {
		t.Fatal(err)
	}
	principal := f.config.Principals[0]
	principal.ID, principal.BearerTokenFile = "other-device", ""
	principal.SigningKeys = []config.SigningKey{{ID: "other-signer", PublicKeyFile: path}}
	f.config.Principals = append(f.config.Principals, principal)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: private}, (&jose.SignerOptions{}).WithHeader("kid", "other-signer"))
	if err != nil {
		t.Fatal(err)
	}
	return &detachedCredentials{realm: f.config.Realm, signer: signer, encrypter: f.credentials.encrypter}
}

func noMailboxJob(t *testing.T, f *fileMailboxFixture, id string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(f.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused mailbox request reached any principal's intake", count, err)
	}
}
