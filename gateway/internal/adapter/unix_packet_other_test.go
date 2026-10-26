//go:build !linux

package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixPacketUnavailableBeforeBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "socket")
	if listener, err := ListenUnixPacket(path); listener != nil || !errors.Is(err, ErrUnixPacketUnsupported) {
		t.Fatal("unsupported record listener accepted", listener, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("unsupported listener created a path", err)
	}
	if err := ServeUnixPacket(context.Background(), nil, nil, nil); !errors.Is(err, ErrUnixPacketUnsupported) {
		t.Fatal("wrong unavailable serve result", err)
	}
}
