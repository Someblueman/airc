//go:build darwin || linux

package adapter

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamRetainedExpiredSendCannotExecute(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	b := redisStreamBroker(f)
	r := redisStreamRequest(t, b, amqpRequest(), "1-0")
	entry := redisStreamEntry(t, f, r, -400)
	now := time.Now()
	old := now.Add(-400 * time.Second)
	m, err := f.v.AuthenticateSigned(string(entry.Payload), old)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := f.s.Admit(t.Context(), m.Identity, m.Request, m.ReplayUntil, old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.AuthenticateDetached(string(entry.Payload), false, now); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("expired executable intent", err)
	}
	for i := range 2 {
		if i == 1 {
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			f.s, err = store.Open(t.Context(), f.c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.s.Close() })
			f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, b.Route.Operations)
			if err != nil {
				t.Fatal(err)
			}
		}
		result, binding, err := f.d.RetainedSignedSend(t.Context(), string(entry.Payload), b.Route.Principal, r.RequestID, b.Route.Target, now)
		if err != nil || binding != f.identity.Binding() || result.Result == nil || !reflect.DeepEqual(*result.Result, before.Result) {
			t.Fatalf("retained result changed: %+v %q %v", result, binding, err)
		}
	}
	for _, principal := range []string{"other"} {
		if _, binding, err := f.d.RetainedSignedSend(t.Context(), string(entry.Payload), principal, r.RequestID, b.Route.Target, now); err == nil || binding != "" {
			t.Fatal("foreign route accepted")
		}
	}
	changed := r
	changed.Message = "conflicting historic intent"
	changedEntry := redisStreamEntry(t, f, changed, -400)
	if _, _, err := f.d.RetainedSignedSend(t.Context(), string(changedEntry.Payload), b.Route.Principal, r.RequestID, b.Route.Target, now); !errors.Is(err, store.ErrConflict) {
		t.Fatal("historic conflict accepted", err)
	}
	unknown := redisStreamRequest(t, b, amqpRequest(), "1-1")
	unknownEntry := redisStreamEntry(t, f, unknown, -400)
	if _, _, err := f.d.RetainedSignedSend(t.Context(), string(unknownEntry.Payload), b.Route.Principal, unknown.RequestID, b.Route.Target, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("expired missing intent admitted", err)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, r.RequestID)
	if err != nil || !reflect.DeepEqual(job, before) {
		t.Fatal("lookup recovery mutated retained job", err)
	}
	if _, err := f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, binding, err := f.d.RetainedSignedSend(t.Context(), string(entry.Payload), b.Route.Principal, r.RequestID, b.Route.Target, now); !errors.Is(err, auth.ErrForbidden) || binding != "" {
		t.Fatal("revoked historical recovery", err)
	}
}
