//go:build unix

package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestPollMutationBindsInnerValidity(t *testing.T) {
	c, signer, recipient := messageFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.PollCreate, core.PollVote, core.PollClose)
	verifier, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	parent := strings.Repeat("a", 32)
	for _, operation := range []core.Operation{core.PollCreate, core.PollVote, core.PollClose} {
		for _, encrypted := range []bool{false, true} {
			for name, mutate := range map[string]func(*core.Request){
				"valid":           func(r *core.Request) {},
				"missing-issued":  func(r *core.Request) { r.IssuedAt = nil },
				"missing-expires": func(r *core.Request) { r.ExpiresAt = nil },
				"changed-issued":  func(r *core.Request) { n := int64(999); r.IssuedAt = &n },
				"changed-expires": func(r *core.Request) { n := int64(1101); r.ExpiresAt = &n },
			} {
				envelope := intent()
				request := core.Request{Version: 1, RequestID: "poll-validity", Operation: operation, Target: &core.Target{Kind: "room", Name: "#Allowed"}, IssuedAt: &envelope.IssuedAt, ExpiresAt: &envelope.ExpiresAt}
				if operation == core.PollCreate {
					request.Poll = &core.PollInput{Question: "choose", Options: []string{"one", "two"}, TTLSeconds: 60}
				} else {
					request.PollID, request.Choice = parent, 1
					if operation == core.PollClose {
						request.Choice = 0
					}
				}
				envelope.Request = request
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
