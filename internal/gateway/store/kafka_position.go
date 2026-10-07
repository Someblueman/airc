package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
)

type KafkaPosition struct {
	NextOffset  int64
	LeaderEpoch int32
}

func cloneKafkaOwners(owners []config.Kafka) []config.Kafka {
	owners = slices.Clone(owners)
	for i := range owners {
		owners[i].Brokers = slices.Clone(owners[i].Brokers)
		owners[i].Route.Operations = slices.Clone(owners[i].Route.Operations)
		if owners[i].Route.InitialOffset != nil {
			position := *owners[i].Route.InitialOffset
			owners[i].Route.InitialOffset = &position
		}
	}
	return owners
}

// kafkaOwner resolves trusted configured ownership and current durable authority.
// The caller holds the store lock. Broker credentials cannot call this through
// external ingress or substitute for signed sender authentication.
func (s *Store) kafkaOwner(ctx context.Context, id string) (config.Kafka, string, error) {
	for _, b := range s.config.Kafka {
		if b.ID != id {
			continue
		}
		for _, p := range s.config.Principals {
			if p.ID != b.Route.Principal || p.Revoked {
				continue
			}
			var revoked bool
			if err := s.db.QueryRowContext(ctx, "SELECT revoked FROM authorization WHERE principal=? AND binding=?", p.ID, p.Binding).Scan(&revoked); err != nil {
				return config.Kafka{}, "", err
			}
			if revoked {
				return config.Kafka{}, "", auth.ErrForbidden
			}
			return b, p.Binding, nil
		}
		return config.Kafka{}, "", auth.ErrForbidden
	}
	return config.Kafka{}, "", ErrNotFound
}

// ProvisionKafka pins one configured partition without dialing or admitting
// work. Existing positions override initial_offset and cannot be rebound.
func (s *Store) ProvisionKafka(ctx context.Context, ownerID string) (KafkaPosition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return KafkaPosition{}, ErrClosed
	}
	b, binding, err := s.kafkaOwner(ctx, ownerID)
	if err != nil {
		return KafkaPosition{}, err
	}
	target, err := json.Marshal(b.Route.Target)
	if err != nil {
		return KafkaPosition{}, ErrInvalid
	}
	var cluster, principal, storedBinding, resultTopic string
	var storedTarget []byte
	var resultPartition int32
	var position KafkaPosition
	err = s.db.QueryRowContext(ctx, `SELECT cluster_id,principal,binding,target,result_topic,result_partition,next_offset,leader_epoch
 FROM kafka_positions WHERE namespace=? AND topic=? AND partition=?`, b.Namespace, b.Route.Topic, b.Route.Partition).Scan(&cluster, &principal, &storedBinding, &storedTarget, &resultTopic, &resultPartition, &position.NextOffset, &position.LeaderEpoch)
	if err == nil {
		if cluster != b.ClusterID || principal != b.Route.Principal || storedBinding != binding || !bytes.Equal(storedTarget, target) || resultTopic != b.Route.ResultTopic || resultPartition != b.Route.ResultPartition {
			return KafkaPosition{}, ErrConflict
		}
		return position, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return KafkaPosition{}, err
	}
	if err := s.checkpoint(ctx); err != nil {
		return KafkaPosition{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KafkaPosition{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM kafka_positions").Scan(&count); err != nil {
		return KafkaPosition{}, err
	}
	if count >= 32 {
		return KafkaPosition{}, ErrCapacity
	}
	position = KafkaPosition{NextOffset: *b.Route.InitialOffset, LeaderEpoch: -1}
	if _, err := tx.ExecContext(ctx, `INSERT INTO kafka_positions VALUES (?,?,?,?,?,?,?,?,?,?,?)`, b.Namespace, b.Route.Topic, b.Route.Partition, b.ClusterID, b.Route.Principal, binding, target, b.Route.ResultTopic, b.Route.ResultPartition, position.NextOffset, position.LeaderEpoch); err != nil {
		return KafkaPosition{}, err
	}
	if err := tx.Commit(); err != nil {
		return KafkaPosition{}, err
	}
	return position, nil
}
