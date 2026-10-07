//go:build darwin || linux

package store

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

func TestKafkaOutcomeBoundsAuthorityAndAtomicPublicationRollback(t *testing.T) {
	c, identity := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.ReserveKafka(t.Context(), "owner", 23, p, kafkaMessage(request(), identity), time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("{"), append([]byte(`"`), append(bytes.Repeat([]byte("a"), auth.MaxSignedBytes), '"')...)} {
		if _, err := s.RetainKafkaOutcome(t.Context(), r.Record, data, time.Unix(1001, 0)); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid whole outcome", err)
		}
	}
	if data, err := s.KafkaOutcome(t.Context(), r.Record); err != nil || data != nil {
		t.Fatal("partial outcome", data, err)
	}
	if _, err := s.RetainKafkaOutcome(t.Context(), KafkaRecord{}, []byte(`{}`), time.Unix(1001, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("forged record", err)
	}
	if _, err := s.RetainKafkaOutcome(t.Context(), r.Record, []byte(`{"whole":true}`), time.Unix(1002, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteKafkaPublication(t.Context(), r.Record, 7, time.Unix(1001, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("publication predates outcome", err)
	}
	// Fail the second write after the position update. Both must roll back.
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER reject_kafka_completion BEFORE UPDATE OF completed_at ON kafka_records BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteKafkaPublication(t.Context(), r.Record, 7, time.Unix(1003, 0)); err == nil {
		t.Fatal("injected failure ignored")
	}
	after, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil || after != p {
		t.Fatal("partial position commit", after, err)
	}
	var pending int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM kafka_records WHERE completed_at IS NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("partial record commit", pending, err)
	}
	if _, err := s.db.ExecContext(t.Context(), `DROP TRIGGER reject_kafka_completion`); err != nil {
		t.Fatal(err)
	}
	otherConfig, _ := kafkaStoreFixture(t)
	other, err := Open(t.Context(), otherConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.KafkaOutcome(t.Context(), r.Record); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-journal handle", err)
	}
	if _, err := s.Revoke(t.Context(), identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KafkaOutcome(t.Context(), r.Record); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revoked outcome", err)
	}
	if _, err := s.CompleteKafkaPublication(t.Context(), r.Record, 7, time.Unix(1003, 0)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revoked completion", err)
	}
	if _, err := s.AdvanceKafkaEmptyRange(t.Context(), "owner", p, 40, 7); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revoked empty range", err)
	}
}
