package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
)

// OSCOREKey binds a canonical cryptographic-context digest to the coap_oscore
// listener it belongs to. Digest must cover the key material, algorithms,
// salt, sender/recipient IDs and context ID, independent of labels. No
// secrets are stored here.
type OSCOREKey struct {
	Digest   [32]byte
	Listener string
}

type OSCOREState struct {
	SenderEnd                        uint64
	ReceiverInitialized              bool
	ReceiverSequence, ReceiverWindow uint64
}

// Match the exclusive sequence ceiling of the pinned libcoap dependency.
const oscoreSequenceLimit = uint64(1<<40) - 1
const maxOSCOREContexts = 1024

// OSCOREReplayWindow is the required native replay_window setting for contexts
// using this store. Smaller native windows discard bits this store retains.
const OSCOREReplayWindow = 64

// ProvisionOSCORE is a trusted owner operation for a newly generated key context.
// Startup and network requests must never invoke it to recover missing state.
// Existing digests cannot be reset or rebound; these records are never pruned.
// The provisioning caller must generate fresh key material before this call.
func (s *Store) ProvisionOSCORE(ctx context.Context, key OSCOREKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if key.Digest == ([32]byte{}) {
		return ErrInvalid
	}
	// The provisioning command checks the listener against the configuration.
	if len(key.Listener) < 1 || len(key.Listener) > 64 {
		return ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return err
	}
	var exists, count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM oscore_state WHERE digest=?", key.Digest[:]).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return ErrConflict
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM oscore_state").Scan(&count); err != nil {
		return err
	}
	if count >= maxOSCOREContexts {
		return ErrCapacity
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO oscore_state(digest,listener,sender_end) VALUES(?,?,0)", key.Digest[:], key.Listener)
	return err
}

// LoadOSCORE refuses absent state and identity mismatches. A missing row is not a
// fresh context; only explicit owner provisioning may create one.
func (s *Store) LoadOSCORE(ctx context.Context, key OSCOREKey) (OSCOREState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return OSCOREState{}, ErrClosed
	}
	return s.loadOSCORE(ctx, key)
}

func (s *Store) loadOSCORE(ctx context.Context, key OSCOREKey) (OSCOREState, error) {
	var state OSCOREState
	var listener string
	var receiver sql.NullInt64
	var window []byte
	err := s.db.QueryRowContext(ctx, "SELECT listener,sender_end,receiver_seq,receiver_window FROM oscore_state WHERE digest=?", key.Digest[:]).Scan(&listener, &state.SenderEnd, &receiver, &window)
	if errors.Is(err, sql.ErrNoRows) {
		return state, ErrNotFound
	}
	if err != nil {
		return state, err
	}
	if listener != key.Listener {
		return OSCOREState{}, ErrConflict
	}
	if state.SenderEnd >= oscoreSequenceLimit {
		return OSCOREState{}, ErrInvalid
	}
	if receiver.Valid {
		if receiver.Int64 < 0 || uint64(receiver.Int64) >= oscoreSequenceLimit || len(window) != 8 {
			return OSCOREState{}, ErrInvalid
		}
		state.ReceiverInitialized = true
		state.ReceiverSequence = uint64(receiver.Int64)
		state.ReceiverWindow = binary.BigEndian.Uint64(window)
		if !validOSCOREWindow(state.ReceiverSequence, state.ReceiverWindow) {
			return OSCOREState{}, ErrInvalid
		}
	} else if window != nil {
		return OSCOREState{}, ErrInvalid
	}
	return state, nil
}

// ReserveOSCORESender commits a strictly increasing exclusive reservation end
// before native encryption. Restart begins at that end, skipping unused values.
// An ambiguous storage error must poison the native context, never permit use.
func (s *Store) ReserveOSCORESender(ctx context.Context, key OSCOREKey, end uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if end == 0 || end >= oscoreSequenceLimit {
		return ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return err
	}
	state, err := s.loadOSCORE(ctx, key)
	if err != nil {
		return err
	}
	if end <= state.SenderEnd {
		return ErrConflict
	}
	_, err = s.db.ExecContext(ctx, "UPDATE oscore_state SET sender_end=? WHERE digest=?", end, key.Digest[:])
	return err
}

// SaveOSCOREReceiver commits only authenticated native replay-state advances,
// before releasing plaintext to application dispatch. It cannot clear a seen bit
// still within the 64-bit window. Repeated identical callbacks are safe.
func (s *Store) SaveOSCOREReceiver(ctx context.Context, key OSCOREKey, sequence, window uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !validOSCOREWindow(sequence, window) {
		return ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return err
	}
	state, err := s.loadOSCORE(ctx, key)
	if err != nil {
		return err
	}
	if state.ReceiverInitialized {
		if sequence < state.ReceiverSequence {
			return ErrConflict
		}
		retained := state.ReceiverWindow << (sequence - state.ReceiverSequence)
		if window&retained != retained {
			return ErrConflict
		}
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], window)
	_, err = s.db.ExecContext(ctx, "UPDATE oscore_state SET receiver_seq=?,receiver_window=? WHERE digest=?", sequence, encoded[:], key.Digest[:])
	return err
}

func validOSCOREWindow(sequence, window uint64) bool {
	return sequence < oscoreSequenceLimit && window&1 != 0 && (sequence >= 63 || window>>(sequence+1) == 0)
}
