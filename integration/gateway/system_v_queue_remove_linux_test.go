//go:build linux && arm64

package gateway_test

import (
	"golang.org/x/sys/unix"
	"testing"
)

func removeSystemVQueue(t *testing.T, id int) {
	t.Helper()
	_, _, errno := unix.Syscall(unix.SYS_MSGCTL, uintptr(id), unix.IPC_RMID, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
}
