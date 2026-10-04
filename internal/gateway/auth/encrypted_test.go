//go:build unix

package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestEncryptedRequiresSignedSender(t *testing.T) {
	c, signer, recipient := messageFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.ProfileUpdate, core.RoomUpdate)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	signed := signIntent(t, signer, intent())
	valid := encryptBytes(t, recipient, signed)
	m, err := v.AuthenticateEncrypted(valid, time.Unix(1000, 0))
	if err != nil || m.Identity.ID() != "device" {
		t.Fatalf("encrypted intent: %v", err)
	}
	model := "model"
	profile := intent()
	profile.Request = core.Request{Version: 1, RequestID: "profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model}, IssuedAt: &profile.IssuedAt, ExpiresAt: &profile.ExpiresAt}
	if _, err := v.AuthenticateEncrypted(encryptBytes(t, recipient, signIntent(t, signer, profile)), time.Unix(1000, 0)); err != nil {
		t.Fatalf("encrypted profile intent: %v", err)
	}
	slow := int64(30)
	room := intent()
	room.Request = core.Request{Version: 1, RequestID: "room", Operation: core.RoomUpdate, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Room: &core.RoomPatch{SlowSeconds: &slow}, IssuedAt: &room.IssuedAt, ExpiresAt: &room.ExpiresAt}
	if _, err := v.AuthenticateEncrypted(encryptBytes(t, recipient, signIntent(t, signer, room)), time.Unix(1000, 0)); err != nil {
		t.Fatalf("encrypted room intent: %v", err)
	}
	room.Request.ExpiresAt = nil
	if _, err := v.AuthenticateEncrypted(encryptBytes(t, recipient, signIntent(t, signer, room)), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("encrypted room without inner validity accepted")
	}
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	for _, inner := range []string{`{"version":1}`, "", signIntent(t, stranger, intent())} {
		if _, err := v.AuthenticateEncrypted(encryptBytes(t, recipient, inner), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
			t.Fatal("decryption authorized unsigned or untrusted sender")
		}
	}
	parts := strings.Split(valid, ".")
	tag, _ := base64.RawURLEncoding.DecodeString(parts[4])
	tag[0] ^= 1
	parts[4] = base64.RawURLEncoding.EncodeToString(tag)
	if _, err := v.AuthenticateEncrypted(strings.Join(parts, "."), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("tampered tag accepted")
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := v.AuthenticateEncrypted(encryptBytes(t, other, signed), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("wrong recipient key accepted")
	}
	parts = strings.Split(valid, ".")
	decoded, _ := base64.RawURLEncoding.DecodeString(parts[0])
	for _, alteration := range [][2]string{{"ECDH-ES", "RSA-OAEP"}, {"A256GCM", "A128GCM"}, {"P-256", "P-384"}, {"recipient", "unknown"}} {
		parts[0] = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(decoded), alteration[0], alteration[1], 1)))
		if _, err := v.AuthenticateEncrypted(strings.Join(parts, "."), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
			t.Fatal("unapproved encrypted header accepted")
		}
	}
}

func FuzzCompactAuthentication(f *testing.F) {
	pub, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	p := &principal{id: "device", binding: "backend", operations: []core.Operation{core.Send}, targets: []core.Target{{Kind: "room", Name: "#Allowed"}}}
	v := &Verifier{profiles: messageProfiles{audience: "fixture", maxLifetime: 86400, signing: map[string]verificationKey{"signer": {public: pub, identity: Identity{principal: p}}}, recipients: map[string]*ecdsa.PrivateKey{"recipient": recipient}}}
	signed := signIntent(f, signer, intent())
	for _, seed := range []string{"", "a.b.c", signed, encryptBytes(f, recipient, signed)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, token string) {
		for _, authenticate := range []func(string, time.Time) (AuthenticatedMessage, error){v.AuthenticateSigned, v.AuthenticateEncrypted} {
			m, err := authenticate(token, time.Unix(1000, 0))
			if err == nil && (m.Identity.ID() != "device" || m.Request.RequestID != "message") {
				t.Fatal("unverified identity or intent")
			}
		}
	})
}
