//go:build darwin || linux

package store

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestKafkaConcurrentReadCopiesRetainOneWholeWinner(t *testing.T) {
	c, identity := kafkaReadStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var records []KafkaRecord
	r := core.Request{Version: 1, RequestID: "read", Operation: core.ContextRead, Target: &c.Kafka[0].Route.Target, ParentID: "0123456789abcdef0123456789abcdef"}
	m := auth.AuthenticatedMessage{Identity: identity, Request: r, ExpiresAt: 1100, ReplayUntil: 1160}
	for _, owner := range []string{"owner", "second"} {
		p, err := s.ProvisionKafka(t.Context(), owner)
		if err != nil {
			t.Fatal(err)
		}
		reservation, err := s.ReserveKafka(t.Context(), owner, 23, p, m, time.Unix(1000, 0))
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, reservation.Record)
	}
	candidates := [][]byte{[]byte(`{"page":["first"],"cursor":"first-cursor"}`), []byte(`{"page":["second"],"cursor":"second-cursor"}`)}
	results := make(chan []byte, 2)
	var wg sync.WaitGroup
	for i, record := range records {
		wg.Go(func() {
			result, err := s.RetainKafkaOutcome(t.Context(), record, candidates[i], time.Unix(1001, 0))
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		})
	}
	wg.Wait()
	close(results)
	var winner []byte
	for result := range results {
		if winner == nil {
			winner = result
		} else if !bytes.Equal(winner, result) {
			t.Fatal("different retained pages", winner, result)
		}
	}
	if !bytes.Equal(winner, candidates[0]) && !bytes.Equal(winner, candidates[1]) {
		t.Fatal("partial winner", winner)
	}
	for _, record := range records {
		cached, err := s.KafkaOutcome(t.Context(), record)
		if err != nil || !bytes.Equal(cached, winner) {
			t.Fatal(cached, err)
		}
		if _, err := s.CompleteKafkaPublication(t.Context(), record, 7, time.Unix(1002, 0)); err != nil {
			t.Fatal(err)
		}
	}
	var jobs int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("read invented Send", jobs, err)
	}
}
