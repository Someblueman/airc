//go:build unix

package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSignedIntentBoundaries(t *testing.T) {
	c, key, _ := messageFixture(t)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	valid := signIntent(t, key, intent())
	for _, now := range []int64{940, 1000, 1160} {
		m, err := v.AuthenticateSigned(valid, time.Unix(now, 0))
		if err != nil || m.Identity.ID() != "device" || m.Request.Message != "first\nsecond" || m.ReplayUntil != 1160 {
			t.Fatalf("valid boundary %d: %+v %v", now, m, err)
		}
	}
	for _, now := range []int64{939, 1161, -1} {
		if _, err := v.AuthenticateSigned(valid, time.Unix(now, 0)); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("invalid time %d accepted", now)
		}
	}
	changes := map[string]func(*MessageEnvelope){
		"audience":                  func(e *MessageEnvelope) { e.Audience = "other" },
		"version":                   func(e *MessageEnvelope) { e.Version = 2 },
		"missing inner validity":    func(e *MessageEnvelope) { e.Request.IssuedAt = nil },
		"mismatched inner validity": func(e *MessageEnvelope) { n := int64(999); e.Request.IssuedAt = &n },
		"lifetime": func(e *MessageEnvelope) {
			e.ExpiresAt = e.IssuedAt + c.Retry.LifetimeSeconds + 1
			e.Request.ExpiresAt = &e.ExpiresAt
		},
		"unauthorized target": func(e *MessageEnvelope) { e.Request.Target.Name = "#Denied" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			e := intent()
			change(&e)
			if _, err := v.AuthenticateSigned(signIntent(t, key, e), time.Unix(1000, 0)); err == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
	read := intent()
	read.Request = core.Request{Version: 1, RequestID: "read", Operation: core.ReceiptGet, LookupID: "message"}
	if _, err := v.AuthenticateSigned(signIntent(t, key, read), time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	read.IssuedAt, read.ExpiresAt = 0, 100
	payload, _ := json.Marshal(read)
	if _, err := v.AuthenticateSigned(signBytes(t, key, payload), time.Unix(50, 0)); err != nil {
		t.Fatal("explicit epoch issuance rejected", err)
	}
	payload = []byte(strings.Replace(string(payload), `"issued_at":0,`, "", 1))
	if _, err := v.AuthenticateSigned(signBytes(t, key, payload), time.Unix(50, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("omitted issuance accepted as epoch zero")
	}
	// Duplicate JSON is rejected even when a trusted signer signed those bytes.
	if _, err := v.AuthenticateSigned(signBytes(t, key, []byte(`{"version":1,"version":1}`)), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("ambiguous signed JSON accepted")
	}
	parts := strings.Split(valid, ".")
	for _, header := range []string{`{"alg":"none","kid":"signer"}`, `{"alg":"EdDSA","kid":"unknown"}`, `{"alg":"EdDSA","kid":"signer","jku":"https://example.invalid"}`, `{"alg":"EdDSA","kid":"signer","kid":"signer"}`} {
		parts[0] = base64.RawURLEncoding.EncodeToString([]byte(header))
		if _, err := v.AuthenticateSigned(strings.Join(parts, "."), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
			t.Fatal("invalid header accepted")
		}
	}
	parts = strings.Split(valid, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	if _, err := v.AuthenticateSigned(strings.Join(parts, "."), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("payload tampering accepted")
	}
	c.Principals[0].Revoked = true
	revoked, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revoked.AuthenticateSigned(valid, time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("revoked signer accepted")
	}
}
