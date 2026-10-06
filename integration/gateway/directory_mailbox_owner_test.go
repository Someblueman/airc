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
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	jose "github.com/go-jose/go-jose/v4"
)

func directoryOtherSigner(t *testing.T, f *directoryMailboxFixture) *detachedCredentials {
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

func noDirectoryMailboxJob(t *testing.T, f *directoryMailboxFixture, id string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(f.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused directory request reached any principal's intake", count, err)
	}
}

func TestDirectoryMailboxRefusesOtherAuthenticatedOwner(t *testing.T) {
	f := prepareDirectoryMailboxFixture(t)
	other := directoryOtherSigner(t, f)
	f.start(t)
	verifier, err := auth.New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	for _, encrypted := range []bool{false, true} {
		request := core.Request{Version: 1, RequestID: "directory-other-owner", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not bypass directory owner"}
		profile := "signed"
		if encrypted {
			profile = "encrypted"
			request.RequestID += "-encrypted"
		}
		token := other.token(t, request, encrypted)
		// Prove the configured other identity really authenticates, rather than
		// treating a malformed or unknown signing key as owner isolation.
		var identity auth.Identity
		if encrypted {
			message, err := verifier.AuthenticateEncrypted(token, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			identity = message.Identity
		} else {
			message, err := verifier.AuthenticateSigned(token, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			identity = message.Identity
		}
		if identity.ID() != "other-device" || identity.Binding() != f.config.Principals[0].Binding {
			t.Fatal("wrong independently verified identity")
		}
		data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: profile, Envelope: token})
		if err != nil {
			t.Fatal(err)
		}
		response := directoryMailboxDocument(t, f, request.RequestID, data)
		if response.CorrelationID != request.RequestID || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
			t.Fatal("other directory owner admitted", response)
		}
		noDirectoryMailboxJob(t, f, request.RequestID)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(native) != 0 {
		t.Fatal("owner refusals changed native history", native)
	}
}
