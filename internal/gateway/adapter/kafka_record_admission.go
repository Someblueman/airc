package adapter

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type kafkaRecordAdmission struct {
	reservation store.KafkaReservation
	binding     string
	operation   core.Operation
	verified    dispatch.VerifiedMessage
}

// reserveKafkaRecord accepts only the original signed intent, not broker
// credentials or a record-derived replacement request ID. The retained path is
// lookup-only Send recovery under current signer and durable route authority.
func reserveKafkaRecord(ctx context.Context, b config.Kafka, d *dispatch.Dispatcher, record kafkawire.Record, expected store.KafkaPosition, now time.Time) (kafkaRecordAdmission, error) {
	if d == nil || record.Offset < expected.NextOffset || !kafkaRequestID(string(record.Key)) || len(record.Value) < 1 || len(record.Value) > kafkawire.MaxValue {
		return kafkaRecordAdmission{}, kafkawire.ErrFrame
	}
	verified, err := d.AuthenticateDetached(string(record.Value), false, now)
	if err != nil {
		if !slices.Contains(b.Route.Operations, core.Send) {
			return kafkaRecordAdmission{}, err
		}
		reservation, binding, err := d.ReserveRetainedKafkaSend(ctx, b.ID, string(record.Key), string(record.Value), record.Offset, expected, now)
		if err != nil {
			return kafkaRecordAdmission{}, err
		}
		return kafkaRecordAdmission{reservation: reservation, binding: binding, operation: core.Send}, nil
	}
	target, ok := verified.Target()
	if !ok || target != b.Route.Target || verified.PrincipalID() != b.Route.Principal || verified.RequestID() != string(record.Key) || !kafkaOperation(verified.Operation()) || !slices.Contains(b.Route.Operations, verified.Operation()) {
		return kafkaRecordAdmission{}, auth.ErrForbidden
	}
	if verified.Operation() != core.Send && !now.Before(verified.ReplayDeadline()) {
		return kafkaRecordAdmission{}, auth.ErrAuthentication
	}
	reservation, err := d.ReserveKafkaVerified(ctx, b.ID, string(record.Key), record.Offset, expected, verified, now)
	if err != nil {
		return kafkaRecordAdmission{}, err
	}
	return kafkaRecordAdmission{reservation: reservation, binding: verified.Binding(), operation: verified.Operation(), verified: verified}, nil
}
