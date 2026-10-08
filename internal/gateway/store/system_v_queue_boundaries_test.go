//go:build darwin || linux

package store

import (
	"errors"
	"math"
	"testing"
)

func TestSystemVQueueOwnershipInvalidAndCorruptRecords(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := systemVQueueRecord()
	mutations := []func(*SystemVQueueOwnership){
		func(r *SystemVQueueOwnership) { r.Listener = "" }, func(r *SystemVQueueOwnership) { r.Listener = "../bad" },
		func(r *SystemVQueueOwnership) { r.Platform = "linux-amd64" }, func(r *SystemVQueueOwnership) { r.Key = 0 }, func(r *SystemVQueueOwnership) { r.ID = -1 }, func(r *SystemVQueueOwnership) { r.ID = math.MaxInt32 + 1 },
		func(r *SystemVQueueOwnership) { r.Changed = 0 }, func(r *SystemVQueueOwnership) { r.BootID = "invalid" }, func(r *SystemVQueueOwnership) { r.BootID = "00000000-0000-0000-0000-000000000000" }, func(r *SystemVQueueOwnership) { r.BootID = "12345678_1234-1234-1234-123456789abc" },
		func(r *SystemVQueueOwnership) { r.NamespaceInode = 0 }, func(r *SystemVQueueOwnership) { r.NamespaceDevice = math.MaxUint64 }, func(r *SystemVQueueOwnership) { r.NamespaceInode = math.MaxUint64 }, func(r *SystemVQueueOwnership) { r.Platform = "darwin-arm64" },
	}
	for _, mutate := range mutations {
		bad := original
		mutate(&bad)
		if err := s.SaveSystemVQueueOwnership(t.Context(), bad, nil); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid record accepted", bad, err)
		}
	}
	if _, err := s.LoadSystemVQueueOwnership(t.Context(), original.Listener); !errors.Is(err, ErrNotFound) {
		t.Fatal("invalid writes persisted", err)
	}
	darwin := original
	darwin.Platform = "darwin-arm64"
	darwin.NamespaceDevice = 0
	darwin.NamespaceInode = 0
	if err := s.SaveSystemVQueueOwnership(t.Context(), darwin, nil); err != nil {
		t.Fatal("valid Darwin profile refused", err)
	}
	loaded, err := s.LoadSystemVQueueOwnership(t.Context(), darwin.Listener)
	if err != nil || loaded != darwin {
		t.Fatal(loaded, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE system_v_queue_ownership SET boot_id=?", "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSystemVQueueOwnership(t.Context(), original.Listener); !errors.Is(err, ErrInvalid) {
		t.Fatal("corrupt record loaded", err)
	}
	if err := s.SaveSystemVQueueOwnership(t.Context(), original, &darwin); !errors.Is(err, ErrInvalid) {
		t.Fatal("corrupt record reset", err)
	}
}
func TestSystemVQueueOwnershipCapacity(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?)
 INSERT INTO system_v_queue_ownership SELECT 'owner-'||x,'linux-arm64',x,0,0,1234,'12345678-1234-1234-1234-123456789abc',4,5 FROM n`, maxSystemVQueueOwners)
	if err != nil {
		t.Fatal(err)
	}
	next := systemVQueueRecord()
	next.Key = math.MaxInt32
	if err := s.SaveSystemVQueueOwnership(t.Context(), next, nil); !errors.Is(err, ErrCapacity) {
		t.Fatal("unbounded records", err)
	}
	old, err := s.LoadSystemVQueueOwnership(t.Context(), "owner-1")
	if err != nil {
		t.Fatal(err)
	}
	next = old
	next.ID++
	if err := s.SaveSystemVQueueOwnership(t.Context(), next, &old); err != nil {
		t.Fatal("bounded replace refused", err)
	}
}
