package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

const kafkaRetainedRows = 16384

// pendingCapacity counts an unfinished read intent once across all record
// copies. Send is charged through its existing delivery job only.
func (s *Store) pendingCapacity(ctx context.Context, tx *sql.Tx, principal string) error {
	var total, owned int
	err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(principal=?),0) FROM (
 SELECT principal FROM jobs WHERE terminal_at IS NULL
 UNION ALL
 SELECT principal FROM kafka_intents ki WHERE operation<>'send'
 AND EXISTS (SELECT 1 FROM kafka_records kr WHERE kr.principal=ki.principal
 AND kr.binding=ki.binding AND kr.request_id=ki.request_id AND kr.completed_at IS NULL)
 )`, principal).Scan(&total, &owned)
	if err != nil {
		return err
	}
	if total >= s.config.Limits.Pending || owned >= s.config.Limits.PendingPerPrincipal {
		return ErrCapacity
	}
	return nil
}

// reserveKafkaInTx requires current route/authority and an owned transaction.
// It neither commits nor changes the partition position.
func (s *Store) reserveKafkaInTx(ctx context.Context, tx *sql.Tx, b config.Kafka, binding string, offset int64, expected KafkaPosition, identity auth.Identity, r core.Request, data []byte, fingerprint string, replay, n, intentAt, retention int64) (KafkaReservation, error) {
	id, err := config.KafkaRecordID(b.Namespace, b.Route.Topic, b.Route.Partition, offset)
	if err != nil {
		return KafkaReservation{}, ErrInvalid
	}
	record := KafkaRecord{owner: s, ownerID: b.ID, offset: offset, expected: expected, principal: identity.ID(), binding: binding, requestID: r.RequestID, recordID: id}
	var oldPrincipal, oldBinding, oldRequest, oldID string
	var prior int64
	var completed sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT principal,binding,request_id,record_id,expected_position,completed_at FROM kafka_records WHERE namespace=? AND topic=? AND partition=? AND offset=?`, b.Namespace, b.Route.Topic, b.Route.Partition, offset).Scan(&oldPrincipal, &oldBinding, &oldRequest, &oldID, &prior, &completed)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return KafkaReservation{}, err
	}
	if exists && (oldPrincipal != identity.ID() || oldBinding != binding || oldRequest != r.RequestID || oldID != id || prior != expected.NextOffset || completed.Valid) {
		return KafkaReservation{}, ErrConflict
	}
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT fingerprint FROM kafka_intents WHERE principal=? AND binding=? AND request_id=?`, identity.ID(), binding, r.RequestID).Scan(&stored)
	newIntent := errors.Is(err, sql.ErrNoRows)
	if err != nil && !newIntent {
		return KafkaReservation{}, err
	}
	if !newIntent && stored != fingerprint {
		return KafkaReservation{}, ErrConflict
	}
	if exists {
		return KafkaReservation{Record: record}, nil
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM kafka_records WHERE namespace=? AND topic=? AND partition=? AND completed_at IS NULL`, b.Namespace, b.Route.Topic, b.Route.Partition).Scan(&pending); err != nil {
		return KafkaReservation{}, err
	}
	if pending != 0 {
		return KafkaReservation{}, ErrConflict
	}
	var records, intents int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM kafka_records),(SELECT count(*) FROM kafka_intents)`).Scan(&records, &intents); err != nil {
		return KafkaReservation{}, err
	}
	if records >= kafkaRetainedRows || newIntent && intents >= kafkaRetainedRows {
		return KafkaReservation{}, ErrCapacity
	}
	if r.Operation != core.Send {
		// An identical pending intent on another partition already owns its charge.
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM kafka_records WHERE principal=? AND binding=? AND request_id=? AND completed_at IS NULL`, identity.ID(), binding, r.RequestID).Scan(&pending); err != nil {
			return KafkaReservation{}, err
		}
		if pending == 0 {
			if err := s.pendingCapacity(ctx, tx, identity.ID()); err != nil {
				return KafkaReservation{}, err
			}
		}
	}
	if newIntent {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kafka_intents (principal,binding,request_id,fingerprint,request,operation,replay_until,admitted_at,retention_seconds) VALUES (?,?,?,?,?,?,?,?,?)`, identity.ID(), binding, r.RequestID, fingerprint, data, r.Operation, replay, intentAt, retention); err != nil {
			return KafkaReservation{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO kafka_records (namespace,topic,partition,offset,record_id,principal,binding,request_id,expected_position,admitted_at,retention_seconds) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, b.Namespace, b.Route.Topic, b.Route.Partition, offset, id, identity.ID(), binding, r.RequestID, expected.NextOffset, n, retention); err != nil {
		return KafkaReservation{}, err
	}
	return KafkaReservation{Record: record, Created: newIntent}, nil
}
