package testmqueue

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// mqueueMagic is the mqueue filesystem's statfs type (linux/magic.h).
const mqueueMagic = 0x19800202

// Require skips the test unless /dev/mqueue is a mounted mqueue filesystem,
// which the POSIX message queue carrier opens to create and inspect queues.
// The kernel may support queues while a container leaves them unmounted.
func Require(t testing.TB) {
	t.Helper()
	var fs unix.Statfs_t
	err := unix.Statfs("/dev/mqueue", &fs)
	if errors.Is(err, unix.ENOENT) || err == nil && fs.Type != mqueueMagic {
		t.Skip("POSIX message queues need the mqueue filesystem at /dev/mqueue; as root: mkdir -p /dev/mqueue && mount -t mqueue none /dev/mqueue")
	}
	if err != nil {
		t.Fatal(err)
	}
}
