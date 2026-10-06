//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"
)

func oscoreKey() OSCOREKey {
	return OSCOREKey{Digest: [32]byte{1}, Principal: "device", Binding: "backend"}
}

func TestOSCOREStateBoundaries(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	key := oscoreKey()
	if _, err := s.LoadOSCORE(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing state", err)
	}
	if err := s.ProvisionOSCORE(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := s.ProvisionOSCORE(t.Context(), key); !errors.Is(err, ErrConflict) {
		t.Fatal("reset permitted", err)
	}
	wrong := key
	wrong.Principal = "other"
	if _, err := s.LoadOSCORE(t.Context(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("identity rebound", err)
	}
	if err := s.ProvisionOSCORE(t.Context(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("digest rebound", err)
	}
	initial, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || initial != (OSCOREState{}) {
		t.Fatal("initial state", initial, err)
	}
	if err := s.ReserveOSCORESender(t.Context(), key, 8); err != nil {
		t.Fatal(err)
	}
	for _, end := range []uint64{0, 7, 8, oscoreSequenceLimit, ^uint64(0)} {
		if err := s.ReserveOSCORESender(t.Context(), key, end); err == nil {
			t.Fatal("invalid sender reservation", end)
		}
	}
	if err := s.SaveOSCOREReceiver(t.Context(), key, 0, 1); err != nil {
		t.Fatal(err)
	}
	zero, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || !zero.ReceiverInitialized || zero.ReceiverSequence != 0 || zero.ReceiverWindow != 1 {
		t.Fatal("zero lost initialization", zero, err)
	}
	if err := s.SaveOSCOREReceiver(t.Context(), key, 1, 3); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][2]uint64{{0, 1}, {1, 1}, {2, 1}, {2, 2}, {1, 7}, {oscoreSequenceLimit, 1}} {
		if err := s.SaveOSCOREReceiver(t.Context(), key, invalid[0], invalid[1]); err == nil {
			t.Fatal("replay state rollback allowed", invalid)
		}
	}
	// Out-of-order authenticated packets can add seen bits without changing head.
	if err := s.SaveOSCOREReceiver(t.Context(), key, 3, 13); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveOSCOREReceiver(t.Context(), key, 3, 15); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveOSCOREReceiver(t.Context(), key, 3, 15); err != nil {
		t.Fatal("idempotent save", err)
	}
	if err := s.SaveOSCOREReceiver(t.Context(), key, 67, 1<<63|1); err != nil {
		t.Fatal(err)
	}
	final, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || final.SenderEnd != 8 || final.ReceiverWindow != 1<<63|1 {
		t.Fatal("unsigned window", final, err)
	}
}

func TestOSCOREStorageFailureDoesNotAdvanceState(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := oscoreKey()
	if err := s.ProvisionOSCORE(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveOSCORESender(t.Context(), key, 8); err == nil {
		t.Fatal("read-only sender write succeeded")
	}
	if err := s.SaveOSCOREReceiver(t.Context(), key, 0, 1); err == nil {
		t.Fatal("read-only receiver write succeeded")
	}
	state, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || state != (OSCOREState{}) {
		t.Fatal("failed persistence changed state", state, err)
	}
}

func TestOSCOREMigrationPreservesDeliveryJournal(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	// Produce the preceding schema without modifying its jobs or identity pin.
	job, _, err := s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE posix_queue_ownership; DROP TABLE oscore_state; PRAGMA user_version=10"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ProvisionOSCORE(t.Context(), oscoreKey()); err != nil {
		t.Fatal(err)
	}
	retained, err := s.Lookup(t.Context(), identity, job.Request.RequestID)
	if err != nil || retained.UpstreamID != job.UpstreamID || retained.Request.Message != job.Request.Message || retained.Result.State != job.Result.State {
		t.Fatal("delivery journal changed", retained, err)
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("migration", version, err)
	}
}
