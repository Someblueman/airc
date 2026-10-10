//go:build unix

package auth

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRetainedSendVerificationDoesNotRenewExecution(t *testing.T) {
	c, key, _ := messageFixture(t)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	token := signIntent(t, key, intent())
	now := time.Unix(2000, 0)
	if _, err := v.AuthenticateSigned(token, now); err == nil {
		t.Fatal("expired intent executable")
	}
	p, err := v.VerifyRetainedSend(token, now)
	if err != nil || p.Request.Operation != core.Send || p.Request.RequestID != intent().Request.RequestID || p.Identity.ID() != "device" {
		t.Fatalf("retained proof: %+v %v", p, err)
	}
	for _, change := range []func(*MessageEnvelope){
		func(e *MessageEnvelope) { e.Audience = "other" },
		func(e *MessageEnvelope) { e.Request.Operation = core.HistoryRead },
		func(e *MessageEnvelope) { e.Request.IssuedAt = nil },
		func(e *MessageEnvelope) { e.Request.Target.Name = "#Denied" },
		func(e *MessageEnvelope) { e.ExpiresAt = e.IssuedAt; e.Request.ExpiresAt = &e.ExpiresAt },
	} {
		e := intent()
		change(&e)
		if _, err := v.VerifyRetainedSend(signIntent(t, key, e), now); err == nil {
			t.Fatal("invalid retained intent accepted")
		}
	}
	for _, n := range []int64{-1, 939} {
		if _, err := v.VerifyRetainedSend(token, time.Unix(n, 0)); err == nil {
			t.Fatal("invalid verification time")
		}
	}
	if _, err := v.VerifyRetainedSend(token[:len(token)-1]+"!", now); err == nil {
		t.Fatal("malformed signature accepted")
	}
	c.Principals[0].Revoked = true
	v, err = New(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.VerifyRetainedSend(token, now); err == nil {
		t.Fatal("configured revocation bypassed")
	}
}
