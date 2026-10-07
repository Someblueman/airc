//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestKafkaReadSharesPendingBudgetAndIntentCharge(t *testing.T) {
	c, identity := kafkaReadStoreFixture(t)
	c.Limits.Pending = 1
	c.Limits.PendingPerPrincipal = 1
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProvisionKafka(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	r := core.Request{Version: 1, RequestID: "read", Operation: core.ContextRead, Target: &c.Kafka[0].Route.Target, ParentID: "0123456789abcdef0123456789abcdef"}
	m := auth.AuthenticatedMessage{Identity: identity, Request: r, ExpiresAt: 1100, ReplayUntil: 1160}
	first, err := s.ReserveKafka(t.Context(), "owner", 23, p, m, time.Unix(1000, 0))
	if err != nil || first.Job != nil || !first.Created {
		t.Fatal(first, err)
	}
	duplicate, err := s.ReserveKafka(t.Context(), "second", 23, p, m, time.Unix(1001, 0))
	if err != nil || duplicate.Created {
		t.Fatal("double charged shared intent", duplicate, err)
	}
	if _, _, err := s.Admit(t.Context(), identity, request(), 0, time.Unix(1001, 0)); !errors.Is(err, ErrCapacity) {
		t.Fatal("ordinary Send ignored read budget", err)
	}
	var intents, records, jobs int
	if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM kafka_intents),(SELECT count(*) FROM kafka_records),(SELECT count(*) FROM jobs)`).Scan(&intents, &records, &jobs); err != nil || intents != 1 || records != 2 || jobs != 0 {
		t.Fatal(intents, records, jobs, err)
	}
	if _, err := s.Maintain(t.Context(), time.Unix(200000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM kafka_records WHERE completed_at IS NULL`).Scan(&records); err != nil || records != 2 {
		t.Fatal("pruned pending reads", records, err)
	}
}
