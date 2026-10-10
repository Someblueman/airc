//go:build unix

package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestTopicMutationBindsInnerValidity(t *testing.T) {
	c, signer, recipient := messageFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.TopicSet)
	verifier, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, encrypted := range []bool{false, true} {
		authenticate := func(envelope MessageEnvelope) error {
			token := signIntent(t, signer, envelope)
			if encrypted {
				_, err := verifier.AuthenticateEncrypted(encryptBytes(t, recipient, token), time.Unix(1000, 0))
				return err
			}
			_, err := verifier.AuthenticateSigned(token, time.Unix(1000, 0))
			return err
		}
		for name, mutate := range map[string]func(*core.Request){
			"valid":          func(r *core.Request) {},
			"missing issue":  func(r *core.Request) { r.IssuedAt = nil },
			"missing expiry": func(r *core.Request) { r.ExpiresAt = nil },
			"changed issue":  func(r *core.Request) { n := int64(999); r.IssuedAt = &n },
			"changed expiry": func(r *core.Request) { n := int64(1101); r.ExpiresAt = &n },
		} {
			envelope := intent()
			topic := "topic"
			envelope.Request = core.Request{Version: 1, RequestID: "topic", Operation: core.TopicSet, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Topic: &topic, IssuedAt: &envelope.IssuedAt, ExpiresAt: &envelope.ExpiresAt}
			mutate(&envelope.Request)
			err := authenticate(envelope)
			if name == "valid" && err != nil || name != "valid" && !errors.Is(err, ErrAuthentication) {
				t.Fatalf("encrypted=%t case=%s err=%v", encrypted, name, err)
			}
		}
	}
}
