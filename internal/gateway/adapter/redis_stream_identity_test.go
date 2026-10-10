//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func TestRedisStreamAdmissionSigningOnlyPrincipal(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	c := f.c
	c.Principals = append([]config.Principal(nil), c.Principals...)
	c.Principals[0].BearerTokenFile = ""
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	broker := redisStreamBroker(f)
	route := broker.Route
	d, err := dispatch.New(f.s, v, f.reader, f.cursors, route.Operations)
	if err != nil {
		t.Fatal(err)
	}
	request := amqpRequest()
	request = redisStreamRequest(t, broker, request, "1-0")
	a := admitRedisStream(t.Context(), d, broker, route.Stream, redisStreamEntry(t, f, request, 0), make(chan struct{}, 1))
	if a.refused || a.failure != nil || a.binding != c.Principals[0].Binding || a.result == nil || a.result.Result == nil || !a.result.Result.Durable {
		t.Fatalf("signing-only admission: %+v", a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.Binding != c.Principals[0].Binding {
		t.Fatal("signed binding changed", err)
	}
}

func TestRedisStreamAdmissionConfiguredRevocationRefusesCorrelation(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	c := f.c
	c.Principals = append([]config.Principal(nil), c.Principals...)
	c.Principals[0].Revoked = true
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	broker := redisStreamBroker(f)
	route := broker.Route
	d, err := dispatch.New(f.s, v, f.reader, f.cursors, route.Operations)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	a := admitRedisStream(t.Context(), d, broker, route.Stream, redisStreamEntry(t, f, redisStreamRequest(t, broker, amqpRequest(), "1-0"), 0), slots)
	if !a.refused || a.result != nil || a.failure != nil || a.requestID != "" || a.binding != "" || len(slots) != 0 {
		t.Fatalf("revoked signature disclosed correlation: %+v", a)
	}
}
