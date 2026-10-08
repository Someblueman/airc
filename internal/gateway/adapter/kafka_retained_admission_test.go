//go:build darwin || linux

package adapter

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaDispatcherRetainedSendNeverAdmitsOrExtendsLease(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	p, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := amqpRequest()
	token := string(redisSignedRequest(t, f, r, -400))
	now := time.Now()
	old := now.Add(-400 * time.Second)
	message, err := f.v.AuthenticateSigned(token, old)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := f.s.Admit(t.Context(), message.Identity, message.Request, message.ReplayUntil, old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.AuthenticateDetached(token, false, now); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("expired executable", err)
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
			f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, f.route().Operations)
			if err != nil {
				t.Fatal(err)
			}
		}
		reservation, binding, err := f.d.ReserveRetainedKafkaSend(t.Context(), "owner", r.RequestID, token, 25, p, now)
		if err != nil || binding != f.identity.Binding() || reservation.Created || reservation.Job == nil || !reflect.DeepEqual(*reservation.Job, before) {
			t.Fatal(reservation, binding, err)
		}
	}
	if _, binding, err := f.d.ReserveRetainedKafkaSend(t.Context(), "owner", "wrong", token, 25, p, now); !errors.Is(err, auth.ErrForbidden) || binding != "" {
		t.Fatal(binding, err)
	}
	changed := r
	changed.Message = "conflicting"
	if _, _, err := f.d.ReserveRetainedKafkaSend(t.Context(), "owner", r.RequestID, string(redisSignedRequest(t, f, changed, -400)), 25, p, now); !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	unknown := r
	unknown.RequestID = "unknown"
	if _, _, err := f.d.ReserveRetainedKafkaSend(t.Context(), "owner", unknown.RequestID, string(redisSignedRequest(t, f, unknown, -400)), 25, p, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	read := core.Request{Version: 1, RequestID: "expired-read", Operation: core.ContextRead, Target: r.Target, ParentID: "0123456789abcdef0123456789abcdef"}
	if _, _, err := f.d.ReserveRetainedKafkaSend(t.Context(), "owner", read.RequestID, string(redisSignedRequest(t, f, read, -400)), 25, p, now); err == nil {
		t.Fatal("expired read admitted")
	}
	after, err := f.s.Lookup(t.Context(), f.identity, r.RequestID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("historical recovery changed job", after, err)
	}
	if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != p {
		t.Fatal("retained reservation advanced", current, err)
	}
	if _, err := f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, binding, err := f.d.ReserveRetainedKafkaSend(t.Context(), "owner", r.RequestID, token, 25, p, now); !errors.Is(err, auth.ErrForbidden) || binding != "" {
		t.Fatal(binding, err)
	}
}
