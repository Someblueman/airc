package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// KafkaRecord is a journal-owned association, not authority to execute intent.
// Its private scope is checked by subsequent journal operations.
type KafkaRecord struct {
	owner                                   *Store
	ownerID                                 string
	offset                                  int64
	expected                                KafkaPosition
	principal, binding, requestID, recordID string
}

func (r KafkaRecord) RecordID() string  { return r.recordID }
func (r KafkaRecord) RequestID() string { return r.requestID }

type KafkaReservation struct {
	Record  KafkaRecord
	Job     *Job // Send only; durable intake is not native acceptance.
	Created bool
}

// ReserveKafka consumes trusted signed codec output from the dispatcher. It
// atomically associates the record and original intent with Send admission.
// Broker values must never be passed here before signed authentication.
func (s *Store) ReserveKafka(ctx context.Context, ownerID string, offset int64, expected KafkaPosition, message auth.AuthenticatedMessage, now time.Time) (KafkaReservation, error) {
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-s.config.Retry.LifetimeSeconds-auth.ClockSkewSeconds || message.ExpiresAt < 0 || message.ExpiresAt > math.MaxInt64-auth.ClockSkewSeconds || message.ReplayUntil != message.ExpiresAt+auth.ClockSkewSeconds || n > message.ReplayUntil {
		return KafkaReservation{}, ErrInvalid
	}
	r, data, fingerprint, err := kafkaIntent(message.Request)
	if err != nil {
		return KafkaReservation{}, err
	}
	if r.Operation == core.Send && (r.IssuedAt == nil || r.ExpiresAt == nil || *r.ExpiresAt != message.ExpiresAt) {
		return KafkaReservation{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, binding, err := s.kafkaReservationOwner(ctx, ownerID, offset, expected, message.Identity, r)
	if err != nil {
		return KafkaReservation{}, err
	}
	if err := s.checkpoint(ctx); err != nil {
		return KafkaReservation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KafkaReservation{}, err
	}
	defer tx.Rollback()
	reservation, err := s.reserveKafkaInTx(ctx, tx, b, binding, offset, expected, message.Identity, r, data, fingerprint, message.ReplayUntil, n, n, s.config.Retry.TerminalRetentionSeconds)
	if err != nil {
		return KafkaReservation{}, err
	}
	if r.Operation == core.Send {
		job, created, err := s.admitInTx(ctx, tx, message.Identity, r, message.ReplayUntil, n, fingerprint)
		if err != nil {
			return KafkaReservation{}, err
		}
		reservation.Job, reservation.Created = &job, created
	}
	if err := tx.Commit(); err != nil {
		return KafkaReservation{}, err
	}
	return reservation, nil
}

// ReserveRetainedKafkaSend associates only an identical retained durable Send.
// It never calls admission or extends the job, replay marker or intent lifetime.
func (s *Store) ReserveRetainedKafkaSend(ctx context.Context, ownerID string, offset int64, expected KafkaPosition, proof auth.RetainedSend, now time.Time) (KafkaReservation, error) {
	if now.Unix() < 0 || proof.Request.Operation != core.Send {
		return KafkaReservation{}, ErrInvalid
	}
	r, data, fingerprint, err := kafkaIntent(proof.Request)
	if err != nil {
		return KafkaReservation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, binding, err := s.kafkaReservationOwner(ctx, ownerID, offset, expected, proof.Identity, r)
	if err != nil {
		return KafkaReservation{}, err
	}
	if err := s.checkpoint(ctx); err != nil {
		return KafkaReservation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KafkaReservation{}, err
	}
	defer tx.Rollback()
	var stored string
	var admitted, retention, replay int64
	err = tx.QueryRowContext(ctx, `SELECT fingerprint,admitted_at,retention_seconds,replay_until FROM jobs WHERE principal=? AND binding=? AND request_id=?`, proof.Identity.ID(), binding, r.RequestID).Scan(&stored, &admitted, &retention, &replay)
	if errors.Is(err, sql.ErrNoRows) {
		return KafkaReservation{}, ErrNotFound
	}
	if err != nil {
		return KafkaReservation{}, err
	}
	if stored != fingerprint {
		return KafkaReservation{}, ErrConflict
	}
	job, err := readJob(ctx, tx, proof.Identity.ID(), binding, r.RequestID)
	if err != nil {
		return KafkaReservation{}, err
	}
	if job.Request.Operation != core.Send || !job.Result.Durable {
		return KafkaReservation{}, ErrInvalid
	}
	reservation, err := s.reserveKafkaInTx(ctx, tx, b, binding, offset, expected, proof.Identity, r, data, fingerprint, replay, now.Unix(), admitted, retention)
	if err != nil {
		return KafkaReservation{}, err
	}
	reservation.Job, reservation.Created = &job, false
	if err := tx.Commit(); err != nil {
		return KafkaReservation{}, err
	}
	return reservation, nil
}

func kafkaIntent(request core.Request) (core.Request, []byte, string, error) {
	// The JSON snapshot owns all optional pointers/slices before normalization.
	data, err := json.Marshal(request)
	if err != nil || len(data) > auth.MaxSignedBytes {
		return core.Request{}, nil, "", ErrInvalid
	}
	var r core.Request
	if err := json.Unmarshal(data, &r); err != nil {
		return core.Request{}, nil, "", ErrInvalid
	}
	if err := r.Normalize(); err != nil || !slices.Contains([]core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}, r.Operation) {
		return core.Request{}, nil, "", ErrInvalid
	}
	data, err = json.Marshal(r)
	if err != nil || len(data) > auth.MaxSignedBytes {
		return core.Request{}, nil, "", ErrInvalid
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		return core.Request{}, nil, "", ErrInvalid
	}
	return r, data, fingerprint, nil
}

func (s *Store) kafkaReservationOwner(ctx context.Context, id string, offset int64, expected KafkaPosition, identity auth.Identity, r core.Request) (config.Kafka, string, error) {
	if s.closed {
		return config.Kafka{}, "", ErrClosed
	}
	if offset < 0 || offset == math.MaxInt64 || expected.NextOffset < 0 || expected.NextOffset > offset || expected.LeaderEpoch < -1 {
		return config.Kafka{}, "", ErrInvalid
	}
	b, binding, err := s.kafkaOwner(ctx, id)
	if err != nil {
		return config.Kafka{}, "", err
	}
	if identity.ID() != b.Route.Principal || identity.Binding() != binding || r.Target == nil || *r.Target != b.Route.Target || !slices.Contains(b.Route.Operations, r.Operation) {
		return config.Kafka{}, "", auth.ErrForbidden
	}
	if err := s.allowed(ctx, identity, r); err != nil {
		return config.Kafka{}, "", err
	}
	position, err := s.kafkaPosition(ctx, b, binding)
	if errors.Is(err, sql.ErrNoRows) {
		return config.Kafka{}, "", ErrNotFound
	}
	if err != nil {
		return config.Kafka{}, "", err
	}
	if position != expected {
		return config.Kafka{}, "", ErrConflict
	}
	return b, binding, nil
}
