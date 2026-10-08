//go:build (darwin || linux) && arm64

package service

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"golang.org/x/sys/unix"
	"testing"
)

func systemVIngressFixture(t *testing.T) (config.Config, *store.Store, int32) {
	t.Helper()
	c := lifecycleConfig(t)
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for range 16 {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			t.Fatal(err)
		}
		key := int32(binary.LittleEndian.Uint32(raw[:]) & 0x7fffffff)
		if key == 0 {
			continue
		}
		q, err := sysvqueue.Create(key)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		return c, s, key
	}
	t.Fatal("unable to reserve fixture key")
	return config.Config{}, nil, 0
}
func TestSystemVIngressRegistersRecreatesAndPinsKey(t *testing.T) {
	_, s, key := systemVIngressFixture(t)
	q, err := openSystemVIngress(t.Context(), s, "sysv", key)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	record, err := s.LoadSystemVQueueOwnership(t.Context(), "sysv")
	if err != nil || record.Key != key || record.ID != int64(q.Identity().ID) || record.Sequence != q.Identity().Sequence || record.Changed != q.Identity().Changed {
		t.Fatal("ownership not registered", record, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = openSystemVIngress(t.Context(), s, "sysv", key)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	after, err := s.LoadSystemVQueueOwnership(t.Context(), "sysv")
	if err != nil || after.ID != int64(q.Identity().ID) || after == record {
		t.Fatal("new lifecycle not registered", after, err)
	}
	otherKey := key - 1
	if otherKey == 0 {
		otherKey = 2
	}
	if other, err := openSystemVIngress(t.Context(), s, "sysv", otherKey); other != nil || !errors.Is(err, store.ErrConflict) {
		t.Fatal("key rebound", other, err)
	}
}
func TestSystemVIngressRefusesUnknownWithoutRemoval(t *testing.T) {
	_, s, key := systemVIngressFixture(t)
	unknown, err := sysvqueue.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	defer unknown.Close()
	if q, err := openSystemVIngress(t.Context(), s, "sysv", key); q != nil || !errors.Is(err, store.ErrConflict) {
		t.Fatal("unknown queue adopted", q, err)
	}
	if err := unknown.WriteMessage(t.Context(), []byte("retained")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSystemVQueueOwnership(t.Context(), "sysv"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("invented ownership", err)
	}
}
func TestSystemVIngressRecoversRecordedQueueAndRefusesReplacement(t *testing.T) {
	_, s, key := systemVIngressFixture(t)
	original, err := openSystemVIngress(t.Context(), s, "sysv", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = original.Close() })
	recovered, err := openSystemVIngress(t.Context(), s, "sysv", key)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Identity() != original.Identity() {
		t.Fatal("wrong lifecycle")
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := sysvqueue.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if q, err := openSystemVIngress(t.Context(), s, "sysv", key); q != nil || !errors.Is(err, sysvqueue.ErrInvalid) {
		t.Fatal("replacement adopted", q, err)
	}
	if err := replacement.WriteMessage(t.Context(), []byte("retained")); err != nil {
		t.Fatal("replacement removed", err)
	}
}
