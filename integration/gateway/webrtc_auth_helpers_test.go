//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	jose "github.com/go-jose/go-jose/v4"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type webRTCOtherPrincipal struct {
	private ed25519.PrivateKey
}

func addWebRTCOtherPrincipal(t *testing.T, c *config.Config) webRTCOtherPrincipal {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(c.Principals[0].BearerTokenFile)
	publicPath := filepath.Join(root, "other-signing.json")
	data, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	bearerPath := filepath.Join(root, "other.token")
	if err := auth.CreateCredential(bearerPath); err != nil {
		t.Fatal(err)
	}
	p := c.Principals[0]
	p.ID = "other"
	p.BearerTokenFile = bearerPath
	p.SigningKeys = []config.SigningKey{{ID: "other-signer", PublicKeyFile: publicPath}}
	p.Operations = append([]core.Operation(nil), dispatch.ImplementedOperations()...)
	p.Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	c.Principals = append(c.Principals, p)
	return webRTCOtherPrincipal{private: private}
}

func webRTCOtherCredentials(t *testing.T, f webRTCFixture, material webRTCOtherPrincipal) *detachedCredentials {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: material.private}, (&jose.SignerOptions{}).WithHeader("kid", "other-signer"))
	if err != nil {
		t.Fatal(err)
	}
	return &detachedCredentials{realm: f.credentials.realm, signer: signer, encrypter: f.credentials.encrypter}
}

func sendWebRTCFrame(t *testing.T, session *webRTCSession, credentials *detachedCredentials, request core.Request, encrypted bool) {
	t.Helper()
	profile := "signed"
	if encrypted {
		profile = "encrypted"
	}
	body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "webrtc-" + request.RequestID, Profile: profile, Envelope: credentials.token(t, request, encrypted)})
	if err != nil {
		t.Fatal(err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range webTransportDatagrams(body, id) {
		if err := session.channel.Send(fragment); err != nil {
			t.Fatal(err)
		}
	}
}

func revokeWebRTCPrincipal(t *testing.T, stateDir, principal string) {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: filepath.Join(stateDir, "journal.sqlite"), RawQuery: "mode=rw&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, "UPDATE authorization SET generation=generation+CASE WHEN revoked=0 THEN 1 ELSE 0 END,revoked=1 WHERE principal=?", principal); err != nil {
		t.Fatal(err)
	}
}
