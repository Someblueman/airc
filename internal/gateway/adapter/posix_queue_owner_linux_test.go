//go:build linux && (arm64 || amd64)

package adapter

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPOSIXQueueExclusiveAttributesAndPermissions(t *testing.T) {
	q := newTestPOSIXQueue(t, 2)
	if duplicate, err := CreatePOSIXQueue(q.name, 2); !errors.Is(err, unix.EEXIST) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatal("adopted existing queue", err)
	}
	if other, err := OpenPOSIXQueue(q.name, 3); !errors.Is(err, ErrPOSIXQueueInvalid) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("accepted wrong depth", err)
	}
	if err := unix.Fchmod(q.fd, 0666); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenPOSIXQueue(q.name, 2); !errors.Is(err, ErrPOSIXQueueInvalid) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("accepted unsafe mode", err)
	}
	if err := q.WriteMessage(t.Context(), []byte("refuse-mode")); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("changed permissions accepted", err)
	}
	if err := unix.Fchmod(q.fd, 0600); err != nil {
		t.Fatal(err)
	}
	if err := q.WriteMessage(t.Context(), []byte("healthy")); err != nil {
		t.Fatal("failed open damaged existing queue", err)
	}
	flags, err := unix.FcntlInt(uintptr(q.fd), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("queue descriptor not close-on-exec", flags, err)
	}
	flags, err = unix.FcntlInt(uintptr(q.directory), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("directory descriptor not close-on-exec", flags, err)
	}
}

func TestPOSIXQueueReplacementRefusesIOAndPreservesNewName(t *testing.T) {
	original := newTestPOSIXQueue(t, 2)
	if err := unix.Unlinkat(original.directory, original.name[1:], 0); err != nil {
		t.Fatal(err)
	}
	// Keep the original open so its inode cannot be recycled as the replacement.
	replacement, err := CreatePOSIXQueue(original.name, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if original.Identity() == replacement.Identity() {
		t.Fatal("replacement reused a live inode")
	}
	if err := original.WriteMessage(t.Context(), []byte("wrong queue")); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("wrote after replacement", err)
	}
	if _, _, err := original.ReadMessage(t.Context(), make([]byte, POSIXQueueMessageBytes)); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("read after replacement", err)
	}
	if err := original.Close(); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("replacement close result", err)
	}
	reopened, err := OpenPOSIXQueue(original.name, 2)
	if err != nil {
		t.Fatal("old close removed replacement", err)
	}
	if reopened.Identity() != replacement.Identity() {
		t.Fatal("new name changed")
	}
	_ = reopened.Close()
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenPOSIXQueue(original.name, 2); !errors.Is(err, unix.ENOENT) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("owned close retained name", err)
	}
}

func TestPOSIXQueueRejectsForeignOwnedQueue(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root in the isolated Linux fixture")
	}
	q := newTestPOSIXQueue(t, 2)
	if err := unix.Fchown(q.fd, 1, 1); err != nil {
		t.Fatal(err)
	}
	// Root could bypass mode bits at mq_open, but the backend must still refuse
	// a descriptor whose kernel owner is a different effective UID.
	if other, err := OpenPOSIXQueue(q.name, 2); !errors.Is(err, ErrPOSIXQueueInvalid) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("foreign-owned queue accepted", err)
	}
	if err := q.WriteMessage(t.Context(), []byte("wrong-owner")); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("changed owner accepted", err)
	}
	if err := q.Close(); err != nil {
		t.Fatal("owned inode cleanup after owner change", err)
	}
}

func TestPOSIXQueueZeroValueRefusesIOAndClose(t *testing.T) {
	q := &POSIXQueue{}
	if err := q.Close(); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("zero queue close", err)
	}
	if err := q.WriteMessage(t.Context(), []byte("body")); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("zero queue write", err)
	}
	if _, _, err := q.ReadMessage(t.Context(), make([]byte, POSIXQueueMessageBytes)); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("zero queue read", err)
	}
}
