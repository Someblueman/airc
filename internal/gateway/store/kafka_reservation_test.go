//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func kafkaMessage(r core.Request, identity auth.Identity) auth.AuthenticatedMessage {
	issued, expires := int64(1000), int64(1100)
	r.IssuedAt, r.ExpiresAt = &issued, &expires
	return auth.AuthenticatedMessage{Identity: identity, Request: r, ExpiresAt: expires, ReplayUntil: expires + auth.ClockSkewSeconds}
}

func TestKafkaReservationAtomicRestartAndConflicts(t *testing.T) {
	c, identity := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	position, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	message := kafkaMessage(request(), identity)
	reserved, err := s.ReserveKafka(t.Context(), "owner", 25, position, message, time.Unix(1000, 0))
	if err != nil || !reserved.Created || reserved.Job == nil || !reserved.Job.Result.Durable || reserved.Record.RecordID() == "" {
		t.Fatal(reserved, err)
	}
	duplicate, err := s.ReserveKafka(t.Context(), "owner", 25, position, message, time.Unix(1001, 0))
	if err != nil || duplicate.Created || duplicate.Record.RecordID() != reserved.Record.RecordID() {
		t.Fatal(duplicate, err)
	}
	changed := message
	changed.Request.Message = "conflict"
	if _, err := s.ReserveKafka(t.Context(), "owner", 25, position, changed, time.Unix(1001, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("changed intent", err)
	}
	if _, err := s.ReserveKafka(t.Context(), "owner", 26, position, message, time.Unix(1001, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("skipped pending record", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil || after != position {
		t.Fatal("reservation advanced", after, err)
	}
	resumed, err := s.ReserveKafka(t.Context(), "owner", 25, position, message, time.Unix(1002, 0))
	if err != nil || resumed.Created || resumed.Job.UpstreamID != reserved.Job.UpstreamID {
		t.Fatal("restart", resumed, err)
	}
	if _, err := s.Revoke(t.Context(), identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveKafka(t.Context(), "owner", 25, position, message, time.Unix(1003, 0)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revocation", err)
	}
}

func TestKafkaReservationRollbackAndRetainedSend(t *testing.T) {
	c, identity := kafkaStoreFixture(t)
	c.Limits.Pending = 1
	c.Limits.PendingPerPrincipal = 1
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	position, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	first := request()
	first.RequestID = "occupied"
	if _, _, err := s.Admit(t.Context(), identity, first, 0, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	message := kafkaMessage(request(), identity)
	if _, err := s.ReserveKafka(t.Context(), "owner", 23, position, message, time.Unix(1000, 0)); !errors.Is(err, ErrCapacity) {
		t.Fatal("quota", err)
	}
	var records, intents int
	if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM kafka_records),(SELECT count(*) FROM kafka_intents)`).Scan(&records, &intents); err != nil || records != 0 || intents != 0 {
		t.Fatal("partial commit", records, intents, err)
	}
	// Existing durable work can be associated without another admission charge.
	proof := auth.RetainedSend{Identity: identity, Request: first}
	reserved, err := s.ReserveRetainedKafkaSend(t.Context(), "owner", 23, position, proof, time.Unix(2000, 0))
	if err != nil || reserved.Created || reserved.Job == nil || reserved.Job.ReplayUntil != 0 {
		t.Fatal("retained", reserved, err)
	}
	if _, err := s.Maintain(t.Context(), time.Unix(200000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(t.Context(), identity, first.RequestID); err != nil {
		t.Fatal("pending association lost job", err)
	}
	proof.Request.Message = "different"
	if _, err := s.ReserveRetainedKafkaSend(t.Context(), "owner", 23, position, proof, time.Unix(200001, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("retained conflict", err)
	}
}
