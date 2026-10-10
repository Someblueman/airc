//go:build darwin || linux

package store

import (
	"errors"
	"sync"
	"testing"
)

func systemVQueueRecord() SystemVQueueOwnership {
	return SystemVQueueOwnership{Listener: "sysv", Platform: "linux-arm64", Key: 512, ID: 0, Sequence: 0, Changed: 1234, BootID: "12345678-1234-1234-1234-123456789abc", NamespaceDevice: 4, NamespaceInode: 5}
}
func TestSystemVQueueOwnershipRestartAndCompareReplace(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	original := systemVQueueRecord()
	if _, err := s.LoadSystemVQueueOwnership(t.Context(), original.Listener); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SaveSystemVQueueOwnership(t.Context(), original, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSystemVQueueOwnership(t.Context(), original, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("unconditional overwrite", err)
	}
	other := original
	other.Listener = "other"
	if err := s.SaveSystemVQueueOwnership(t.Context(), other, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("key rebound", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadSystemVQueueOwnership(t.Context(), original.Listener)
	if err != nil || loaded != original {
		t.Fatal("restart lost ownership", loaded, err)
	}
	next := original
	next.ID++
	next.Sequence++
	next.Changed++
	stale := original
	stale.NamespaceInode++
	if err := s.SaveSystemVQueueOwnership(t.Context(), next, &stale); !errors.Is(err, ErrConflict) {
		t.Fatal("stale replace", err)
	}
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() { results <- s.SaveSystemVQueueOwnership(t.Context(), next, &original) })
	}
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("compare-and-replace winners", winners)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = s.LoadSystemVQueueOwnership(t.Context(), original.Listener)
	if err != nil || loaded != next {
		t.Fatal("replacement lost", loaded, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSystemVQueueOwnership(t.Context(), next.Listener); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := s.SaveSystemVQueueOwnership(t.Context(), next, &next); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func TestSystemVQueueOwnershipRegistrationFailureRetainsRecord(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := systemVQueueRecord()
	if err := s.SaveSystemVQueueOwnership(t.Context(), original, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_system_v BEFORE INSERT ON system_v_queue_ownership BEGIN SELECT RAISE(FAIL,'refuse'); END`); err != nil {
		t.Fatal(err)
	}
	next := original
	next.ID++
	if err := s.SaveSystemVQueueOwnership(t.Context(), next, &original); err == nil {
		t.Fatal("injected failure succeeded")
	}
	loaded, err := s.LoadSystemVQueueOwnership(t.Context(), original.Listener)
	if err != nil || loaded != original {
		t.Fatal("failed replace changed ownership", loaded, err)
	}
}
