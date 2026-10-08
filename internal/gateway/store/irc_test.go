//go:build darwin || linux

package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

func TestIRCAtomicReplayRestartConflictAndRevocation(t *testing.T) {
	c, _, _ := fixture(t)
	stamp := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	proof := ircProof(t, c, "device", "stable", " exact 世界  ", stamp)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := s.AdmitIRC(t.Context(), proof, stamp.Add(time.Second))
	if err != nil || !created || job.Request.Message != proof.Body() {
		t.Fatal("whole admission", created, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			j, created, err := s.AdmitIRC(t.Context(), proof, stamp.Add(2*time.Second))
			if err != nil || created || j.UpstreamID != job.UpstreamID {
				t.Error("replay changed job", created, err)
			}
		})
	}
	workers.Wait()
	for _, changed := range []auth.VerifiedIRCEvent{ircProof(t, c, "device", "stable", "changed", stamp), ircProof(t, c, "other", "stable", proof.Body(), stamp), ircProof(t, c, "device", "stable", proof.Body(), stamp.Add(time.Millisecond))} {
		if _, _, err := s.AdmitIRC(t.Context(), changed, stamp.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
			t.Fatal("changed content/authority/time accepted", err)
		}
	}
	if _, _, err := s.AdmitIRC(t.Context(), proof, proof.ReplayDeadline()); err != auth.ErrAuthentication {
		t.Fatal("expired proof renewed", err)
	}
	if _, _, err := s.AdmitIRC(t.Context(), proof, stamp.Add(-time.Millisecond)); err != auth.ErrAuthentication {
		t.Fatal("future proof accepted", err)
	}
	if _, err := s.Revoke(t.Context(), "device"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitIRC(t.Context(), proof, stamp.Add(2*time.Second)); err != auth.ErrForbidden {
		t.Fatal("revoked replay accepted", err)
	}
	var jobs, events int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM irc_events").Scan(&events); err != nil || jobs != 1 || events != 1 {
		t.Fatal("partial/conflicting intake", jobs, events, err)
	}
}

func TestIRCPrunedResultAndFailedTransaction(t *testing.T) {
	c, _, _ := fixture(t)
	stamp := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	proof := ircProof(t, c, "device", "stable", "whole", stamp)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// An event-row failure must roll back the native job in the same transaction.
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_irc BEFORE INSERT ON irc_events BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitIRC(t.Context(), proof, stamp.Add(time.Second)); err == nil {
		t.Fatal("injected failure ignored")
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial job", count, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER refuse_irc"); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitIRC(t.Context(), proof, stamp.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DELETE FROM jobs WHERE request_id=?", job.Request.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitIRC(t.Context(), proof, stamp.Add(2*time.Second)); err != ErrConflict {
		t.Fatal("pruned result recreated", err)
	}
	var empty auth.VerifiedIRCEvent
	if _, _, err := s.AdmitIRC(t.Context(), empty, stamp); err != auth.ErrAuthentication {
		t.Fatal("zero proof", err)
	}
}
