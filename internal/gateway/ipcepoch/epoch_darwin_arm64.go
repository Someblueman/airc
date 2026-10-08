//go:build darwin && arm64

package ipcepoch

import (
	"golang.org/x/sys/unix"
	"strings"
)

// Current reads the kernel's read-only boot-session UUID. Darwin System V
// identifiers inhabit the kernel's shared IPC space; there is no Linux nsfs.
func Current() (Epoch, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return Epoch{}, err
	}
	boot = strings.ToLower(boot)
	if !validBootID(boot) {
		return Epoch{}, ErrInvalid
	}
	return Epoch{BootID: boot}, nil
}
