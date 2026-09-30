package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListenUnixUsesRestrictivePermissionsAndPreservesActiveSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "airc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	first, err := ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("socket mode = %04o, want 0600", got)
	}
	if second, err := ListenUnix(path); err == nil {
		_ = second.Close()
		t.Fatal("ListenUnix replaced an active socket")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active socket path was removed: %v", err)
	}
}
