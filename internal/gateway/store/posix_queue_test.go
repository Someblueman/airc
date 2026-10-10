//go:build darwin || linux

package store

import (
	"errors"
	"math"
	"testing"
)

func queueOwnershipRecord() POSIXQueueOwnership {
	return POSIXQueueOwnership{Listener: "queue", Name: "/agw-ingress", Depth: 8, BootID: "12345678-1234-1234-1234-123456789abc", NamespaceDevice: 4, NamespaceInode: 5, QueueDevice: 6, QueueInode: 7}
}

func TestPOSIXQueueOwnershipRestartAndReplacement(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	original := queueOwnershipRecord()
	if _, err := s.LoadPOSIXQueueOwnership(t.Context(), original.Listener); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), original, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), original, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("unconditional overwrite", err)
	}
	other := original
	other.Listener = "other"
	if err := s.SavePOSIXQueueOwnership(t.Context(), other, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("name rebound", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadPOSIXQueueOwnership(t.Context(), original.Listener)
	if err != nil || loaded != original {
		t.Fatal("restart lost ownership", loaded, err)
	}
	next := original
	next.QueueInode++
	stale := original
	stale.NamespaceInode++
	if err := s.SavePOSIXQueueOwnership(t.Context(), next, &stale); !errors.Is(err, ErrConflict) {
		t.Fatal("stale replace", err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), next, &original); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), original, &original); !errors.Is(err, ErrConflict) {
		t.Fatal("rollback without current record", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = s.LoadPOSIXQueueOwnership(t.Context(), original.Listener)
	if err != nil || loaded != next {
		t.Fatal("replacement lost", loaded, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadPOSIXQueueOwnership(t.Context(), original.Listener); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), next, &next); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestPOSIXQueueOwnershipInvalidRecords(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := queueOwnershipRecord()
	mutations := []func(*POSIXQueueOwnership){
		func(r *POSIXQueueOwnership) { r.Listener = "" }, func(r *POSIXQueueOwnership) { r.Listener = "../bad" },
		func(r *POSIXQueueOwnership) { r.Name = "/../bad" }, func(r *POSIXQueueOwnership) { r.Name = "/queue\x00" },
		func(r *POSIXQueueOwnership) { r.Depth = 0 }, func(r *POSIXQueueOwnership) { r.Depth = 11 },
		func(r *POSIXQueueOwnership) { r.BootID = "invalid" }, func(r *POSIXQueueOwnership) { r.BootID = "12345678x1234-1234-1234-123456789abc" },
		func(r *POSIXQueueOwnership) { r.NamespaceInode = 0 }, func(r *POSIXQueueOwnership) { r.QueueInode = 0 },
		func(r *POSIXQueueOwnership) { r.QueueDevice = math.MaxUint64 },
	}
	for _, mutate := range mutations {
		bad := original
		mutate(&bad)
		if err := s.SavePOSIXQueueOwnership(t.Context(), bad, nil); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid record accepted", bad, err)
		}
	}
	if _, err := s.LoadPOSIXQueueOwnership(t.Context(), original.Listener); !errors.Is(err, ErrNotFound) {
		t.Fatal("invalid writes persisted", err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), original, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE posix_queue_ownership SET boot_id=?", "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadPOSIXQueueOwnership(t.Context(), original.Listener); !errors.Is(err, ErrInvalid) {
		t.Fatal("corrupt record loaded", err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), original, &original); !errors.Is(err, ErrInvalid) {
		t.Fatal("corrupt record reset", err)
	}
}

func TestPOSIXQueueOwnershipCapacity(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?)
 INSERT INTO posix_queue_ownership SELECT 'owner-'||x,'/owned-'||x,1,'12345678-1234-1234-1234-123456789abc',1,2,3,x FROM n`, maxPOSIXQueueOwners)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), queueOwnershipRecord(), nil); !errors.Is(err, ErrCapacity) {
		t.Fatal("unbounded owners", err)
	}
	old, err := s.LoadPOSIXQueueOwnership(t.Context(), "owner-1")
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.QueueInode++
	if err := s.SavePOSIXQueueOwnership(t.Context(), next, &old); err != nil {
		t.Fatal("bounded replacement refused", err)
	}
}
