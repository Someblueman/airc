//go:build darwin || linux

package adapter

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInheritedUnixStreamRejectsUnconnectedDescriptor(t *testing.T) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	source := os.NewFile(uintptr(fd), "unconnected")
	defer source.Close()
	stream, err := OpenInheritedUnixStream(source)
	if stream != nil {
		stream.Close()
	}
	if err == nil {
		t.Fatal("accepted unconnected Unix endpoint")
	}
}
