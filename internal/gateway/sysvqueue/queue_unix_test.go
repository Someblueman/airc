//go:build (darwin && arm64) || (linux && (amd64 || arm64))

package sysvqueue

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"testing"
	"time"
	"unsafe"
)

func TestActualQueueWholePacketsAndOwnership(t *testing.T) {
	q := fixture(t)
	if q.Identity().Changed < time.Now().Add(-3*time.Second).Unix() {
		t.Fatal("metadata ABI timestamp changed", q.Identity())
	}
	if _, err := Create(q.Identity().Key); !errors.Is(err, unix.EEXIST) {
		t.Fatal("adopted existing queue", err)
	}
	peer, err := Open(q.Identity().Key)
	if err != nil || peer.Identity() != q.Identity() {
		t.Fatal(peer, err)
	}
	body := make([]byte, MessageBytes)
	for i := range body {
		body[i] = byte(i)
	}
	if err := peer.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ReadMessage(t.Context(), make([]byte, MessageBytes-1)); err != ErrInvalid {
		t.Fatal("short buffer accepted", err)
	}
	received := make([]byte, MessageBytes)
	n, err := q.ReadMessage(t.Context(), received)
	if err != nil || n != MessageBytes || string(received) != string(body) {
		t.Fatal("whole packet changed", n, err)
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.WriteMessage(t.Context(), []byte("owner remains")); err != nil {
		t.Fatal("peer removed owner queue", err)
	}
	if _, err := peer.ReadMessage(t.Context(), received); !errors.Is(err, net.ErrClosed) {
		t.Fatal("closed peer usable", err)
	}
	if err := q.WriteMessage(t.Context(), make([]byte, MessageBytes+1)); err != ErrInvalid {
		t.Fatal("oversize write accepted", err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(q.Identity().Key); !errors.Is(err, unix.ENOENT) {
		t.Fatal("owned key survived", err)
	}
	if _, err := Create(0); err != ErrInvalid {
		t.Fatal("private dynamic key accepted", err)
	}
	var zero Queue
	if err := zero.Close(); err != ErrInvalid {
		t.Fatal("zero owner accepted", err)
	}
}

func TestQueuePressureAndCancellation(t *testing.T) {
	q := fixture(t)
	body := make([]byte, MessageBytes)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	sent := 0
	for sent < 100 {
		err := q.WriteMessage(ctx, body)
		if errors.Is(err, context.DeadlineExceeded) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		sent++
	}
	if sent < 1 || sent >= 100 {
		t.Fatal("pressure not bounded", sent)
	}
	for range sent {
		if _, err := q.ReadMessage(t.Context(), body); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if err := q.WriteMessage(cancelled, []byte("refused")); err != context.Canceled {
		t.Fatal("cancelled write", err)
	}
	empty, finish := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer finish()
	if _, err := q.ReadMessage(empty, body); err != context.DeadlineExceeded {
		t.Fatal("empty receive did not expire", err)
	}
	if _, err := q.ReadMessage(cancelled, body); err != context.Canceled {
		t.Fatal("cancelled read", err)
	}
}

func TestKernelOversizeIsNeverTruncatedOrConsumed(t *testing.T) {
	q := fixture(t)
	var oversized struct {
		Type int64
		Body [MessageBytes + 1]byte
	}
	oversized.Type = 1
	_, _, errno := nativeSend(uintptr(q.Identity().ID), uintptr(unsafe.Pointer(&oversized)), MessageBytes+1)
	if errno != 0 {
		t.Fatal(errno)
	}
	if _, err := q.ReadMessage(t.Context(), make([]byte, MessageBytes)); !errors.Is(err, unix.E2BIG) {
		t.Fatal("oversize packet truncated", err)
	}
	var received struct {
		Type int64
		Body [MessageBytes + 1]byte
	}
	n, _, errno := nativeReceive(uintptr(q.Identity().ID), uintptr(unsafe.Pointer(&received)), MessageBytes+1)
	if errno != 0 || n != MessageBytes+1 {
		t.Fatal("oversize packet consumed", n, errno)
	}
}
