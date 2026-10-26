//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/testfs"
)

// The parent establishes the named connection before passing its accepted
// endpoint. The gateway itself never binds or dials this socket path.
func inheritedNamedUnixFiles(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	root, err := testfs.MkdirTemp("agw-inherited-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(listener.Addr().String(), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	child, err := server.File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Close() })
	parent, err := client.File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Close() })
	return child, parent
}
