//go:build darwin || linux

package store

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestKafkaPositionRestartAndImmutableScope(t *testing.T) {
	c, _ := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	position, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil || position != (KafkaPosition{NextOffset: 23, LeaderEpoch: -1}) {
		t.Fatal("initial position", position, err)
	}
	// Simulate a committed later position, without adding an advancement API yet.
	if _, err := s.db.ExecContext(t.Context(), "UPDATE kafka_positions SET next_offset=41,leader_epoch=7"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Kafka[0].ID = "renamed"
	c.Kafka[0].Brokers[0].Address = "127.0.0.1:9094"
	*c.Kafka[0].Route.InitialOffset = 0
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	position, err = s.ProvisionKafka(t.Context(), "renamed")
	if err != nil || position != (KafkaPosition{NextOffset: 41, LeaderEpoch: 7}) {
		t.Fatal("retained position reset", position, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cluster", "principal", "result_topic", "result_partition", "target"} {
		// Each reopen retains the same existing namespace/topic/partition key.
		changed := c
		changed.Kafka = cloneKafkaOwners(c.Kafka)
		switch field {
		case "cluster":
			changed.Kafka[0].ClusterID = "other-cluster"
		case "principal":
			changed.Kafka[0].Route.Principal = "other"
		case "result_topic":
			changed.Kafka[0].Route.ResultTopic = "other-results"
		case "result_partition":
			changed.Kafka[0].Route.ResultPartition = 4
		case "target":
			target := core.Target{Kind: "room", Name: "#Other"}
			changed.Principals = append(changed.Principals[:0:0], c.Principals...)
			changed.Principals[0].Targets = append(changed.Principals[0].Targets[:0:0], changed.Principals[0].Targets...)
			changed.Principals[0].Targets = append(changed.Principals[0].Targets, target)
			changed.Kafka[0].Route.Target = target
		}
		s, err = Open(t.Context(), changed)
		if err != nil {
			t.Fatal(field, err)
		}
		if _, err := s.ProvisionKafka(t.Context(), "renamed"); !errors.Is(err, ErrConflict) {
			t.Fatal("changed scope accepted", field, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKafkaPositionAuthorityAndSnapshot(t *testing.T) {
	c, _ := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	*c.Kafka[0].Route.InitialOffset = 99
	c.Kafka[0].Route.Topic = "changed"
	c.Kafka[0].Route.Operations[0] = core.ReceiptGet
	position, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil || position.NextOffset != 23 {
		t.Fatal("caller changed provisioned route", position, err)
	}
	if _, err := s.ProvisionKafka(t.Context(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown owner", err)
	}
	if _, err := s.Revoke(t.Context(), "device"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProvisionKafka(t.Context(), "owner"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revoked owner loaded position", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProvisionKafka(t.Context(), "owner"); !errors.Is(err, ErrClosed) {
		t.Fatal("closed store", err)
	}
}
