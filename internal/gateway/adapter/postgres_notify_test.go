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
	return config.PostgresNotifyRoute{Principal: "producer", Target: core.Target{Kind: "room", Name: "#fixture"}}
}

func TestPostgresNotifyAdmitsPlainRequestAsRoutePrincipal(t *testing.T) {
	f := newRouteFixture(t)
	requests := make(chan struct{}, 1)
	for range 2 {
		if err := admitPostgresNotify(t.Context(), f.d, postgresFixtureRoute(), "native", []byte(plainRouteSend), requests); err != nil {
			t.Fatal(err)
		}
	}
	job, err := f.s.Lookup(t.Context(), f.producer, "plain-route")
	if err != nil || !job.Result.Durable || job.Principal != "producer" || job.Request.Target == nil || *job.Request.Target != postgresFixtureRoute().Target || job.Request.Message != "first\nsecond 🙂" {
		t.Fatal("wrong durable intake", job, err)
	}
	if f.jobs(t, "plain-route") != 1 || len(requests) != 0 {
		t.Fatal("replay duplicated intake or leaked capacity")
	}
}

func TestPostgresNotifyRefusesBeforeJournal(t *testing.T) {
	for name, tc := range map[string]struct {
		payload string
		mutate  func(*config.PostgresNotifyRoute, *string)
	}{
		// The principal may send to #other, so only the route check refuses it.
		"target":    {payload: `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#other"},"message":"hi"}`},
		"operation": {payload: `{"version":1,"request_id":"plain-route","operation":"receipt.get","lookup_id":"missing"}`},
		"malformed": {payload: `{"version":1,"request_id":"plain-route","operation":"send",`},
		"unknown":   {payload: plainRouteSend, mutate: func(r *config.PostgresNotifyRoute, _ *string) { r.Principal = "unknown" }},
		"revoked":   {payload: plainRouteSend, mutate: func(r *config.PostgresNotifyRoute, _ *string) { r.Principal = "retired" }},
		"binding":   {payload: plainRouteSend, mutate: func(_ *config.PostgresNotifyRoute, b *string) { *b = "elsewhere" }},
		"route":     {payload: plainRouteSend, mutate: func(r *config.PostgresNotifyRoute, _ *string) { r.Target.Name = "#other" }},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRouteFixture(t)
			route, binding := postgresFixtureRoute(), "native"
			if tc.mutate != nil {
				tc.mutate(&route, &binding)
			}
			requests := make(chan struct{}, 1)
			if err := admitPostgresNotify(t.Context(), f.d, route, binding, []byte(tc.payload), requests); err != nil {
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
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := admitPostgresNotify(ctx, f.d, postgresFixtureRoute(), "native", []byte(plainRouteSend), requests); err != nil || len(requests) != 1 {
		t.Fatal("capacity wait", err, len(requests))
	}
	<-requests
	if f.jobs(t, "plain-route") != 0 {
		t.Fatal("admitted without capacity")
	}
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := admitPostgresNotify(t.Context(), f.d, postgresFixtureRoute(), "native", []byte(plainRouteSend), requests); !errors.Is(err, store.ErrClosed) {
		t.Fatal("closed journal must end the owner", err)
	}
}
