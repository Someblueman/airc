//go:build (darwin && arm64) || (linux && (amd64 || arm64))

package sysvqueue

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/ipcepoch"
	"testing"
)

func TestRecoveryRefusesStaleEpochAndIdentity(t *testing.T) {
	q := fixture(t)
	epoch, err := ipcepoch.Current()
	if err != nil {
		t.Fatal(err)
	}
	again, err := ipcepoch.Current()
	if err != nil || again != epoch {
		t.Fatal("unstable epoch", again, err)
	}
	staleBoot := epoch
	staleBoot.BootID = "00000000-0000-0000-0000-000000000000"
	staleNamespace := epoch
	staleNamespace.NamespaceInode++
	id, seq, changed := q.Identity(), q.Identity(), q.Identity()
	id.ID++
	seq.Sequence++
	changed.Changed++
	for _, bad := range []struct {
		identity Identity
		epoch    ipcepoch.Epoch
	}{{q.Identity(), staleBoot}, {q.Identity(), staleNamespace}, {id, epoch}, {seq, epoch}, {changed, epoch}, {Identity{}, epoch}} {
		if recovered, err := Resume(bad.identity, bad.epoch); recovered != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("adopted stale queue", recovered, err)
		}
		if err := q.WriteMessage(t.Context(), []byte("retained")); err != nil {
			t.Fatal("refusal removed live queue", err)
		}
		if _, err := q.ReadMessage(t.Context(), make([]byte, 1000)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRecoveryRefusesReplacementWithoutRemovingIt(t *testing.T) {
	original := fixture(t)
	epoch, err := ipcepoch.Current()
	if err != nil {
		t.Fatal(err)
	}
	identity := original.Identity()
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := Create(identity.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Identity() == identity {
		t.Fatal("kernel reused exact lifecycle")
	}
	if q, err := Resume(identity, epoch); q != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("adopted replacement", q, err)
	}
	if err := replacement.WriteMessage(t.Context(), []byte("retained")); err != nil {
		t.Fatal("replacement removed", err)
	}
}
