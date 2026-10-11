package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func postgresFixtureRoute() config.PostgresNotifyRoute {
	return config.PostgresNotifyRoute{Target: core.Target{Kind: "room", Name: "#fixture"}}
}

const postgresAuthorless = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"bound"}`

func TestPostgresNotifyAdmitsPlainRequestAsItsAuthor(t *testing.T) {
	f := newRouteFixture(t)
	d := f.route(t, "pg", core.Send)
	requests := make(chan struct{}, 1)
	guests := f.author(t, "", false, true)
	for range 2 {
		if err := admitPostgresNotify(t.Context(), d, guests, "route:pg", postgresFixtureRoute(), []byte(plainRouteSend), requests); err != nil {
			t.Fatal(err)
		}
	}
	if a := f.owner(t, "route-guest", false, "plain-route"); a != (core.Author{Nick: "route-guest"}) {
		t.Fatal("wrong author", a)
	}
	if f.jobs(t, "plain-route") != 1 || len(requests) != 0 {
		t.Fatal("replay duplicated intake or leaked capacity")
	}
	// A request without an author posts as the route's bound author.
	bound := f.author(t, "pg-bot", true, false)
	body := []byte(`{"version":1,"request_id":"bound-route","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"bound"}`)
	if err := admitPostgresNotify(t.Context(), d, bound, "route:pg", postgresFixtureRoute(), body, requests); err != nil {
		t.Fatal(err)
	}
	if a := f.owner(t, "pg-bot", true, "bound-route"); a != (core.Author{Nick: "pg-bot", Registered: true}) {
		t.Fatal("wrong bound author", a)
	}
}

func TestPostgresNotifyRefusesBeforeJournal(t *testing.T) {
	for name, tc := range map[string]struct {
		payload string
		bound   bool
	}{
		"target":         {payload: `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#other"},"message":"hi","author":{"nick":"route-guest"}}`},
		"operation":      {payload: `{"version":1,"request_id":"plain-route","operation":"receipt.get","lookup_id":"missing","author":{"nick":"route-guest"}}`},
		"malformed":      {payload: `{"version":1,"request_id":"plain-route","operation":"send",`},
		"anonymous":      {payload: postgresAuthorless},
		"guest_on_bound": {payload: plainRouteSend, bound: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRouteFixture(t)
			d := f.route(t, "pg", core.Send)
			author := f.author(t, "", false, true)
			if tc.bound {
				author = f.author(t, "pg-bot", true, false)
			}
			requests := make(chan struct{}, 1)
			if err := admitPostgresNotify(t.Context(), d, author, "route:pg", postgresFixtureRoute(), []byte(tc.payload), requests); err != nil {
				t.Fatal("refusal ended the owner", err)
			}
			if f.jobs(t, "plain-route") != 0 || len(requests) != 0 {
				t.Fatal("refusal reached the journal or leaked capacity")
			}
		})
	}
}

func TestPostgresNotifyCapacityAndClosedJournal(t *testing.T) {
	f := newRouteFixture(t)
	d := f.route(t, "pg", core.Send)
	author := f.author(t, "", false, true)
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := admitPostgresNotify(ctx, d, author, "route:pg", postgresFixtureRoute(), []byte(plainRouteSend), requests); err != nil || len(requests) != 1 {
		t.Fatal("capacity wait", err, len(requests))
	}
	<-requests
	if f.jobs(t, "plain-route") != 0 {
		t.Fatal("admitted without capacity")
	}
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := admitPostgresNotify(t.Context(), d, author, "route:pg", postgresFixtureRoute(), []byte(plainRouteSend), requests); !errors.Is(err, store.ErrClosed) {
		t.Fatal("closed journal must end the owner", err)
	}
}
