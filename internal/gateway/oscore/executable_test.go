//go:build darwin || linux

package oscore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeExecutablePermissionBoundary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "worker")
	if err := os.WriteFile(path, []byte("unused"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedExecutable(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0770); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedExecutable(path); err == nil {
		t.Fatal("writable native executable accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedExecutable(path); err == nil {
		t.Fatal("nonexecutable accepted")
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedExecutable(link); err == nil {
		t.Fatal("leaf symlink accepted")
	}
	if err := os.Chmod(root, 0777); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, 0700) }()
	if _, err := ownedExecutable(path); err == nil {
		t.Fatal("replaceable executable path accepted")
	}
}
