package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestVerifiedIntentRejectsTransferAndExpiredExecution(t *testing.T) {
	owner, other := &Dispatcher{}, &Dispatcher{}
	verified := VerifiedMessage{owner: owner, verifiedAt: 100, message: auth.AuthenticatedMessage{ReplayUntil: 200}}
	for name, test := range map[string]struct {
		d *Dispatcher
		v VerifiedMessage
		n int64
	}{
		"zero":                        {owner, VerifiedMessage{}, 150},
		"different_dispatcher":        {other, verified, 150},
		"nil_dispatcher":              {nil, verified, 150},
		"clock_before_authentication": {owner, verified, 99},
		"negative_clock":              {owner, verified, -1},
		"expired":                     {owner, verified, 201},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := test.d.DetachedVerified(context.Background(), test.v, time.Unix(test.n, 0)); !errors.Is(err, auth.ErrAuthentication) {
				t.Fatalf("detached intent rejection: %v", err)
			}
			if err := test.d.DetachedWatchVerified(context.Background(), test.v, time.Unix(test.n, 0), nil); !errors.Is(err, auth.ErrAuthentication) {
				t.Fatalf("watch intent rejection: %v", err)
			}
		})
	}
	for _, now := range []int64{100, 200} {
		if _, err := verified.forDispatcher(owner, time.Unix(now, 0)); err != nil {
			t.Fatalf("valid boundary rejected at %d: %v", now, err)
		}
	}
	route := VerifiedMessage{owner: owner, verifiedAt: 100}
	if _, err := route.forDispatcher(owner, time.Unix(1<<40, 0)); err != nil {
		t.Fatalf("route intent without a validity window expired: %v", err)
	}
	if _, err := route.forDispatcher(owner, time.Unix(99, 0)); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatalf("route intent before authentication: %v", err)
	}
}

func TestVerifiedTargetIsACopy(t *testing.T) {
	if _, ok := (VerifiedMessage{}).Target(); ok {
		t.Fatal("zero intent has a target")
	}
	want := core.Target{Kind: "room", Name: "#fixture"}
	v := VerifiedMessage{message: auth.AuthenticatedMessage{Request: core.Request{Target: &want}}}
	got, ok := v.Target()
	if !ok || got != want {
		t.Fatal("missing authenticated target")
	}
	got.Name = "#other"
	if *v.message.Request.Target != want {
		t.Fatal("inspection mutated intent")
	}
}
