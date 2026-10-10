//go:build darwin || linux

package adapter

import (
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestNATSAdmissionConnectPrincipalMustMatchSignedPrincipal(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	c := f.c
	c.Principals = append([]config.Principal(nil), c.Principals...)
	p := c.Principals[0]
	p.ID, p.SigningKeys = "other", nil
	p.BearerTokenFile = filepath.Join(c.StateDir, "other-bearer")
	if err := auth.CreateCredential(p.BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	c.Principals = append(c.Principals, p)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(p.BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	route := natsAdmissionRoute(f)
	route.Principal = identity.ID()
	slots := make(chan struct{}, 1)
	a := admitNATS(t.Context(), f.d, identity, route, natsAdmissionFrame(t, f, amqpRequest(), 0), slots)
	if !a.refused || a.result != nil || a.failure != nil || a.requestID != "" || len(slots) != 0 {
		t.Fatalf("principal mismatch: %+v", a)
	}
}

func TestNATSAdmissionDurableRevocationRejectsNewAndReplay(t *testing.T) {
	for _, replay := range []bool{false, true} {
		f := newAMQPAdmissionFixture(t)
		request := amqpRequest()
		request.RequestID = "nats-revocation"
		frame := natsAdmissionFrame(t, f, request, 0)
		route := natsAdmissionRoute(f)
		slots := make(chan struct{}, 1)
		if replay {
			a := admitNATS(t.Context(), f.d, f.identity, route, frame, slots)
			if a.result == nil || a.failure != nil || a.refused {
				t.Fatalf("initial admission: %+v", a)
			}
		}
		if _, err := f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
			t.Fatal(err)
		}
		a := admitNATS(t.Context(), f.d, f.identity, route, frame, slots)
		if a.refused || a.result != nil || a.failure == nil || a.failure.Code != core.Forbidden || a.failure.Transient || a.requestID != request.RequestID || len(slots) != 0 {
			t.Fatalf("revoked admission: %+v", a)
		}
	}
}

func TestNATSAdmissionConnectBindingMustMatchSignedBinding(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	c := f.c
	c.Bindings = append([]config.Binding(nil), c.Bindings...)
	c.Principals = append([]config.Principal(nil), c.Principals...)
	c.Bindings[0].ID = "other-binding"
	c.Principals[0].Binding = "other-binding"
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
	if identity.ID() != f.identity.ID() || identity.Binding() == f.identity.Binding() {
		t.Fatal("fixture did not vary binding")
	}
	slots := make(chan struct{}, 1)
	a := admitNATS(t.Context(), f.d, identity, natsAdmissionRoute(f), natsAdmissionFrame(t, f, amqpRequest(), 0), slots)
	if !a.refused || a.result != nil || a.failure != nil || a.requestID != "" || len(slots) != 0 {
		t.Fatalf("binding mismatch: %+v", a)
	}
}
