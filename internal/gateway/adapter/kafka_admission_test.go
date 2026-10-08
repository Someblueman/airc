//go:build darwin || linux

package adapter

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaDispatcherSignedReservationPreservesOriginalIntent(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	p, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := amqpRequest()
	r.RequestID = "producer-chosen-id"
	token := string(redisSignedRequest(t, f, r, 0))
	now := time.Now()
	verified, err := f.d.AuthenticateDetached(token, false, now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := dispatch.New(f.s, f.v, f.reader, f.cursors, f.route().Operations)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		d     *dispatch.Dispatcher
		v     dispatch.VerifiedMessage
		key   string
		owner string
		want  error
	}{
		{"zero", f.d, dispatch.VerifiedMessage{}, r.RequestID, "owner", auth.ErrAuthentication},
		{"foreign dispatcher", other, verified, r.RequestID, "owner", auth.ErrAuthentication},
		{"wrong key", f.d, verified, "wrong", "owner", auth.ErrForbidden},
		{"unknown owner", f.d, verified, r.RequestID, "missing", store.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.d.ReserveKafkaVerified(t.Context(), test.owner, test.key, 25, p, test.v, now); !errors.Is(err, test.want) {
				t.Fatal(err)
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, r.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal admitted", err)
			}
		})
	}
	reserved, err := f.d.ReserveKafkaVerified(t.Context(), "owner", r.RequestID, 25, p, verified, now)
	if err != nil || !reserved.Created || reserved.Job == nil || !reserved.Job.Result.Durable || reserved.Record.RequestID() != r.RequestID || reserved.Job.Request.RequestID != r.RequestID {
		t.Fatal(reserved, err)
	}
	repeat, err := f.d.ReserveKafkaVerified(t.Context(), "owner", r.RequestID, 25, p, verified, now)
	if err != nil || repeat.Created || repeat.Job.UpstreamID != reserved.Job.UpstreamID {
		t.Fatal(repeat, err)
	}
	changed := r
	changed.Message = "changed"
	different, err := f.d.AuthenticateDetached(string(redisSignedRequest(t, f, changed, 0)), false, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.ReserveKafkaVerified(t.Context(), "owner", r.RequestID, 25, p, different, now); !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != p {
		t.Fatal("reservation advanced", current, err)
	}
	if _, err := f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.ReserveKafkaVerified(t.Context(), "owner", r.RequestID, 25, p, verified, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal(err)
	}
}

func TestKafkaDispatcherReadReservationDoesNotExecuteOrCreateSend(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	p, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{Version: 1, RequestID: "read", Operation: core.ContextRead, Target: &f.c.Kafka[0].Route.Target, ParentID: "0123456789abcdef0123456789abcdef"}
	now := time.Now()
	v, err := f.d.AuthenticateDetached(string(redisSignedRequest(t, f, r, 0)), false, now)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := f.d.ReserveKafkaVerified(t.Context(), "owner", r.RequestID, 23, p, v, now)
	if err != nil || !reserved.Created || reserved.Job != nil {
		t.Fatal(reserved, err)
	}
	if _, err := f.s.Lookup(t.Context(), f.identity, r.RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("read invented delivery", err)
	}
	if outcome, err := f.s.KafkaOutcome(t.Context(), reserved.Record); err != nil || outcome != nil {
		t.Fatal("reservation executed read", outcome, err)
	}
}
