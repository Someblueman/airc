package ipcepoch

import "errors"

var ErrInvalid = errors.New("invalid kernel IPC epoch")
var ErrUnsupported = errors.New("kernel IPC epoch unsupported on this platform")

// Epoch scopes persistent identifiers to a boot and, on Linux, IPC namespace.
// It is kernel lifecycle metadata, never producer authentication.
type Epoch struct {
	BootID                          string
	NamespaceDevice, NamespaceInode uint64
}

func validBootID(boot string) bool {
	if len(boot) != 36 {
		return false
	}
	for i, c := range boot {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return boot != "00000000-0000-0000-0000-000000000000"
}
