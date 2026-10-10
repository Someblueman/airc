//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// signedRequestFixture is a protocol-independent compact JWS of a contract
// request, valid for five minutes from now.
func signedRequestFixture(t *testing.T, f amqpAdmissionFixture, r core.Request) []byte {
	t.Helper()
	issued, expires := time.Now().Unix(), time.Now().Unix()+300
	if r.Operation == core.Send {
		r.IssuedAt, r.ExpiresAt = &issued, &expires
	}
	body, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: f.c.Realm, IssuedAt: issued, ExpiresAt: expires, Request: r})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := f.signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return []byte(token)
}
