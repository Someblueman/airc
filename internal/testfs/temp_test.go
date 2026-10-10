//go:build darwin || linux

package testfs

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMkdirTempPrivateSocketRoot(t *testing.T) {
	base, err := os.MkdirTemp("", "testfs-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("TMPDIR", base)
	dir, err := MkdirTemp("socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	wantRoot := base
	if runtime.GOOS == "darwin" {
		wantRoot = "/tmp"
	}
	if filepath.Dir(dir) != wantRoot {
		t.Fatalf("directory parent = %q, want %q", filepath.Dir(dir), wantRoot)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions = %04o, want 0700", info.Mode().Perm())
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
