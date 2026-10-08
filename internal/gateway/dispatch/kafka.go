package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ReserveKafkaVerified associates only intent authenticated by this dispatcher.
// The record key must be the original signed request ID. The journal checks the
// configured owner, current authority, exact position and immutable fingerprint;
// Send admission commits with the association, while reads create no delivery.
// This reservation neither executes a read nor publishes or advances a record.
func (d *Dispatcher) ReserveKafkaVerified(ctx context.Context, ownerID, key string, offset int64, expected store.KafkaPosition, verified VerifiedMessage, now time.Time) (store.KafkaReservation, error) {
	message, err := verified.forDispatcher(d, now)
	if err != nil {
		return store.KafkaReservation{}, err
	}
	if key == "" || key != message.Request.RequestID {
		return store.KafkaReservation{}, auth.ErrForbidden
	}
	if !slices.Contains(d.operations, message.Request.Operation) {
		return store.KafkaReservation{}, ErrUnsupported
	}
	if d.journal == nil {
		return store.KafkaReservation{}, store.ErrInvalid
	}
	return d.journal.ReserveKafka(ctx, ownerID, offset, expected, message, now)
}

// ReserveRetainedKafkaSend verifies an original signed Send under current signer
// authority before associating it with an identical durable job. It cannot admit
// new work, extend a replay/job lease or execute a read. The returned binding is
// authenticated scope for result signing, not broker credential authority.
func (d *Dispatcher) ReserveRetainedKafkaSend(ctx context.Context, ownerID, key, token string, offset int64, expected store.KafkaPosition, now time.Time) (store.KafkaReservation, string, error) {
	if d == nil || d.verifier == nil || !slices.Contains(d.operations, core.Send) {
		return store.KafkaReservation{}, "", ErrUnsupported
	}
	proof, err := d.verifier.VerifyRetainedSend(token, now)
	if err != nil {
		return store.KafkaReservation{}, "", err
	}
	if key == "" || key != proof.Request.RequestID {
		return store.KafkaReservation{}, "", auth.ErrForbidden
	}
	if d.journal == nil {
		return store.KafkaReservation{}, "", store.ErrInvalid
	}
	reservation, err := d.journal.ReserveRetainedKafkaSend(ctx, ownerID, offset, expected, proof, now)
	if err != nil {
		return store.KafkaReservation{}, "", err
	}
	return reservation, proof.Identity.Binding(), nil
}
