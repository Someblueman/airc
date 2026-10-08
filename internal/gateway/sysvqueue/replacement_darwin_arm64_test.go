//go:build darwin && arm64

package sysvqueue

import (
	"errors"
	"golang.org/x/sys/unix"
	"testing"
	"unsafe"
)

func TestRemovedIdentifierNeverAdoptsReplacement(t *testing.T) {
	original := fixture(t)
	key := original.Identity().Key
	_, _, errno := libcCall3(libcMsgctl, uintptr(original.Identity().ID), unix.IPC_RMID, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	replacement, err := Create(key)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Identity().ID == original.Identity().ID {
		t.Fatal("kernel unexpectedly reused exact identifier")
	}
	if err := original.WriteMessage(t.Context(), []byte("wrong owner")); err == nil {
		t.Fatal("removed identifier wrote replacement")
	}
	if err := original.Close(); err == nil {
		t.Fatal("removed owner silently adopted replacement")
	}
	if err := replacement.WriteMessage(t.Context(), []byte("still live")); err != nil {
		t.Fatal("old cleanup removed replacement", err)
	}
}

func TestPermissionChangeRefusesExistingAndNewHandles(t *testing.T) {
	q := fixture(t)
	var raw [116]byte
	id := uintptr(q.Identity().ID)
	_, _, errno := libcCall3(libcMsgctl, id, unix.IPC_STAT, uintptr(unsafe.Pointer(&raw)))
	if errno != 0 {
		t.Fatal(errno)
	}
	raw[16] = 0x84 // 0604 instead of 0600
	_, _, errno = libcCall3(libcMsgctl, id, unix.IPC_SET, uintptr(unsafe.Pointer(&raw)))
	if errno != 0 {
		t.Fatal(errno)
	}
	if _, err := Open(q.Identity().Key); !errors.Is(err, ErrInvalid) {
		t.Fatal("public queue opened", err)
	}
	if err := q.WriteMessage(t.Context(), []byte("refused")); !errors.Is(err, ErrInvalid) {
		t.Fatal("changed permissions accepted", err)
	}
	// Restore permissions, then explicitly remove the fixture: IPC_SET may also
	// change creation metadata, so the original handle must not adopt that lifecycle.
	raw[16] = 0x80
	_, _, errno = libcCall3(libcMsgctl, id, unix.IPC_SET, uintptr(unsafe.Pointer(&raw)))
	if errno != 0 {
		t.Fatal(errno)
	}
	_, _, errno = libcCall3(libcMsgctl, id, unix.IPC_RMID, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	if err := q.Close(); err == nil {
		t.Fatal("removed queue cleanup adopted lifecycle")
	}
}
