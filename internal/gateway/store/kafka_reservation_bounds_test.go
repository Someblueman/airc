//go:build darwin || linux

package store

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

func TestKafkaReservationScopeLeaseAndClosedRefusals(t *testing.T) {
	c, identity := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := KafkaPosition{NextOffset: 23, LeaderEpoch: -1}
	message := kafkaMessage(request(), identity)
	if _, err := s.ReserveKafka(t.Context(), "owner", 23, p, message, time.Unix(1000, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatal("implicit provisioning", err)
	}
	if _, err := s.ProvisionKafka(t.Context(), "owner"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		offset   int64
		position KafkaPosition
	}{
		{"negative", -1, p}, {"overflow", math.MaxInt64, p}, {"before position", 22, p},
		{"stale position", 23, KafkaPosition{NextOffset: 22, LeaderEpoch: -1}},
		{"stale epoch", 23, KafkaPosition{NextOffset: 23, LeaderEpoch: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.ReserveKafka(t.Context(), "owner", tc.offset, tc.position, message, time.Unix(1000, 0)); err == nil {
				t.Fatal("invalid scope accepted")
			}
		})
	}
	changed := message
	changed.Request.Target = &c.Principals[1].Targets[0]
	changed.Identity = auth.Identity{}
	if _, err := s.ReserveKafka(t.Context(), "owner", 23, p, changed, time.Unix(1000, 0)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("empty identity", err)
	}
	changed = message
	changed.ReplayUntil++
	if _, err := s.ReserveKafka(t.Context(), "owner", 23, p, changed, time.Unix(1000, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("untrusted lease", err)
	}
	if _, err := s.ReserveKafka(t.Context(), "owner", 23, p, message, time.Unix(1161, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("expired lease", err)
	}
	proof := auth.RetainedSend{Identity: identity, Request: message.Request}
	if _, err := s.ReserveRetainedKafkaSend(t.Context(), "owner", 23, p, proof, time.Unix(1200, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown historical Send admitted", err)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM kafka_records").Scan(&count); err != nil || count != 0 {
		t.Fatal("refusal wrote record", count, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveKafka(t.Context(), "owner", 23, p, message, time.Unix(1000, 0)); !errors.Is(err, ErrClosed) {
		t.Fatal("closed", err)
	}
}
