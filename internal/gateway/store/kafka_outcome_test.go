//go:build darwin || linux

package store

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestKafkaWholeOutcomeRestartPublicationAndEmptyRange(t *testing.T) {
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
	m := kafkaMessage(request(), identity)
	reservation, err := s.ReserveKafka(t.Context(), "owner", 25, p, m, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteKafkaPublication(t.Context(), reservation.Record, 7, time.Unix(1001, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("advanced without outcome", err)
	}
	if _, err := s.AdvanceKafkaEmptyRange(t.Context(), "owner", p, 40, 7); !errors.Is(err, ErrConflict) {
		t.Fatal("skipped pending", err)
	}
	outcome := []byte(`{"version":1,"operation":"send","result":{"durable":true}}`)
	saved, err := s.RetainKafkaOutcome(t.Context(), reservation.Record, outcome, time.Unix(1001, 0))
	if err != nil || !bytes.Equal(saved, outcome) {
		t.Fatal(saved, err)
	}
	saved[0] = '!'
	winner, err := s.RetainKafkaOutcome(t.Context(), reservation.Record, []byte(`{"different":true}`), time.Unix(1002, 0))
	if err != nil || !bytes.Equal(winner, outcome) {
		t.Fatal("replaced winner", winner, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	current, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil || current != p {
		t.Fatal("pre-publication crash advanced", current, err)
	}
	reservation, err = s.ReserveKafka(t.Context(), "owner", 25, p, m, time.Unix(1003, 0))
	if err != nil {
		t.Fatal(err)
	}
	cached, err := s.KafkaOutcome(t.Context(), reservation.Record)
	if err != nil || !bytes.Equal(cached, outcome) {
		t.Fatal("lost replay outcome", cached, err)
	}
	advanced, err := s.CompleteKafkaPublication(t.Context(), reservation.Record, 7, time.Unix(1004, 0))
	if err != nil || advanced != (KafkaPosition{NextOffset: 26, LeaderEpoch: 7}) {
		t.Fatal(advanced, err)
	}
	again, err := s.CompleteKafkaPublication(t.Context(), reservation.Record, 7, time.Unix(1005, 0))
	if err != nil || again != advanced {
		t.Fatal("publication retry", again, err)
	}
	if _, err := s.AdvanceKafkaEmptyRange(t.Context(), "owner", advanced, 40, 6); err == nil {
		t.Fatal("epoch regression")
	}
	empty, err := s.AdvanceKafkaEmptyRange(t.Context(), "owner", advanced, 40, 8)
	if err != nil || empty != (KafkaPosition{NextOffset: 40, LeaderEpoch: 8}) {
		t.Fatal(empty, err)
	}
	if _, err := s.AdvanceKafkaEmptyRange(t.Context(), "owner", advanced, 50, 8); !errors.Is(err, ErrConflict) {
		t.Fatal("stale empty proof", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	final, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil || final != empty {
		t.Fatal("post-publication crash reset", final, err)
	}
}
