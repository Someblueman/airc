//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestReadGrantPermissionLifetimeCloneAndSingleConsumption(t *testing.T) {
	for _, operation := range []core.Operation{core.HistoryRead, core.MembersRead, core.EventsWatch} {
		t.Run(string(operation), func(t *testing.T) { testReadGrantLifetime(t, operation) })
	}
}

func testReadGrantLifetime(t *testing.T, operation core.Operation) {
	c, _, _ := fixture(t)
	identity := fixtureAuthor()
	s, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "read", Operation: operation, Target: &core.Target{Kind: "room", Name: "#Allowed"}}
	if operation == core.HistoryRead || operation.IsWatch() {
		r.Limit = 1
	}
	now := time.Now()
	for _, cursor := range []string{"../state", "@*", "bad", "fffffffffffffffffffffffffffffffz"} {
		bad := r
		bad.Cursor = cursor
		if _, err := s.AuthorizeAuthorRead(context.Background(), identity, bad, now); !errors.Is(err, ErrInvalid) {
			t.Fatal("untrusted raw cursor accepted", cursor, err)
		}
	}
	g, err := s.AuthorizeAuthorRead(context.Background(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	r.Target.Name = "#Changed"
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			a, err := g.Consume(now)
			if err == nil && (a.Request.Target.Name != "#Allowed" || a.Deadline != now.Add(15*time.Second) || a.Backend != *c.Upstream) {
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
	expired, err := s.AuthorizeAuthorRead(context.Background(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expired.Consume(now.Add(15 * time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatal("expired grant consumed", err)
	}
	if _, err := s.AuthorizeAuthorRead(context.Background(), Submission{}, r, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty identity authorized", err)
	}
}
