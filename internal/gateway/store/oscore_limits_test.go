//go:build darwin || linux

package store

import (
	"errors"
	"testing"
)

func TestOSCOREContextCapacityAndCorruptReplayState(t *testing.T) {
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
	if err := s.SaveOSCOREReceiver(t.Context(), key, 0, 1); err != nil {
		t.Fatal(err)
	}
	// Syntactically valid SQLite bytes can still violate OSCORE's replay window.
	if _, err := s.db.ExecContext(t.Context(), "UPDATE oscore_state SET receiver_window=? WHERE digest=?", []byte{0, 0, 0, 0, 0, 0, 0, 3}, key.Digest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadOSCORE(t.Context(), key); !errors.Is(err, ErrInvalid) {
		t.Fatal("corrupt replay state loaded", err)
	}
	if err := s.ReserveOSCORESender(t.Context(), key, 8); !errors.Is(err, ErrInvalid) {
		t.Fatal("corrupt context allowed encryption reservation", err)
	}
	_, err = s.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1023)
INSERT INTO oscore_state(digest,principal,binding,sender_end) SELECT CAST(printf('%032d',x) AS BLOB),'device','backend',0 FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	other := key
	other.Digest = [32]byte{2}
	if err := s.ProvisionOSCORE(t.Context(), other); !errors.Is(err, ErrCapacity) {
		t.Fatal("unbounded contexts", err)
	}
}
