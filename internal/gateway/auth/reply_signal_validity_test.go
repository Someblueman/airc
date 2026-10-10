//go:build unix

package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestReplySignalMutationBindsInnerValidity(t *testing.T) {
	c, signer, recipient := messageFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.ReplySignalSet, core.ReplySignalCancel)
	verifier, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		for _, encrypted := range []bool{false, true} {
			for name, mutate := range map[string]func(*core.Request){
				"valid":           func(r *core.Request) {},
				"missing-issued":  func(r *core.Request) { r.IssuedAt = nil },
				"missing-expires": func(r *core.Request) { r.ExpiresAt = nil },
				"changed-issued":  func(r *core.Request) { n := int64(999); r.IssuedAt = &n },
				"changed-expires": func(r *core.Request) { n := int64(1101); r.ExpiresAt = &n },
			} {
				envelope := intent()
				parent := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				input := (*core.ReplySignalInput)(nil)
				if operation == core.ReplySignalSet {
					input = &core.ReplySignalInput{Note: "ready", TTLSeconds: 30}
				}
				envelope.Request = core.Request{Version: 1, RequestID: "reply-signal-validity", Operation: operation, Target: &core.Target{Kind: "room", Name: "#Allowed"}, ParentID: parent, ReplySignal: input, IssuedAt: &envelope.IssuedAt, ExpiresAt: &envelope.ExpiresAt}
				mutate(&envelope.Request)
				token := signIntent(t, signer, envelope)
				if encrypted {
					_, err = verifier.AuthenticateEncrypted(encryptBytes(t, recipient, token), time.Unix(1000, 0))
				} else {
					_, err = verifier.AuthenticateSigned(token, time.Unix(1000, 0))
				}
				if name == "valid" && err != nil || name != "valid" && !errors.Is(err, ErrAuthentication) {
					t.Fatalf("op=%s encrypted=%t case=%s err=%v", operation, encrypted, name, err)
				}
			}
		}
	}
}
