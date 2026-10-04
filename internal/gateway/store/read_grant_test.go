//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestReadGrantPermissionLifetimeCloneAndSingleConsumption(t *testing.T) {
	c, stale, _ := fixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.HistoryRead)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Limit: 1}
	now := time.Now()
	if _, err := s.AuthorizeRead(context.Background(), stale, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("credential permission expanded", err)
	}
	for _, cursor := range []string{"../state", "@*", "bad", "fffffffffffffffffffffffffffffffz"} {
		bad := r
		bad.Cursor = cursor
		if _, err := s.AuthorizeRead(context.Background(), identity, bad, now); !errors.Is(err, ErrInvalid) {
			t.Fatal("untrusted raw cursor accepted", cursor, err)
		}
	}
	g, err := s.AuthorizeRead(context.Background(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	r.Target.Name = "#Changed"
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			a, err := g.Consume(now)
			if err == nil && (a.Request.Target.Name != "#Allowed" || a.Deadline != now.Add(15*time.Second) || a.Backend != c.Bindings[0]) {
				t.Error("grant snapshot changed", a)
			}
			results <- err
		})
	}
	wg.Wait()
	one, two := <-results, <-results
	if !(one == nil && errors.Is(two, ErrNotReady) || two == nil && errors.Is(one, ErrNotReady)) {
		t.Fatal("grant consumed more than once", one, two)
	}
	r.Target.Name = "#Allowed"
	expired, err := s.AuthorizeRead(context.Background(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expired.Consume(now.Add(15 * time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatal("expired grant consumed", err)
	}
	s.config.Principals[0].Operations = []core.Operation{core.Send}
	if _, err := s.AuthorizeRead(context.Background(), identity, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("current config permission bypassed", err)
	}
	if _, err := s.AuthorizeRead(context.Background(), auth.Identity{}, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("empty identity authorized", err)
	}
}
