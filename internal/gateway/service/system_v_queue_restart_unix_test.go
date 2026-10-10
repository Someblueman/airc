//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package service

import (
	"bytes"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"golang.org/x/sys/unix"
	"testing"
)

func TestSystemVIngressRecoversRecordedPacketAfterJournalReopen(t *testing.T) {
	c, s, key := systemVIngressFixture(t)
	original, err := openSystemVIngress(t.Context(), s, "sysv", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = original.Close() })
	body := make([]byte, 1000)
	for i := range body {
		body[i] = byte(i)
	}
	if err := original.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := openSystemVIngress(t.Context(), reopened, "sysv", key)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Identity() != original.Identity() {
		t.Fatal("recorded lifecycle changed")
	}
	got := make([]byte, 1000)
	n, err := recovered.ReadMessage(t.Context(), got)
	if err != nil || n != 1000 || !bytes.Equal(body, got) {
		t.Fatal("queued packet changed", n, err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if q, err := sysvqueue.Open(key); q != nil || !errors.Is(err, unix.ENOENT) {
		t.Fatal("recovered ingress remains", q, err)
	}
	if err := original.Close(); err == nil {
		t.Fatal("old owner silently accepted removal")
	}
}
