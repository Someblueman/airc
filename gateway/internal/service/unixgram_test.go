package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Someblueman/airc/internal/testfs"
)

func TestUnixgramOwnerPinsPrivateIngressInode(t *testing.T) {
	if !unixgramOwnerAvailable() {
		t.Skip("unixgram unavailable")
	}
	root, err := testfs.MkdirTemp("agw-svc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	ingressPath := filepath.Join(root, "ingress")
	owner, err := listenUnixgramOwner(ingressPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info := mustLstat(t, ingressPath); info.Mode().Perm() != 0600 || !os.SameFile(info, owner.file) {
		t.Fatal("ingress socket not private or not pinned", info.Mode())
	}
	if err := owner.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(ingressPath); !os.IsNotExist(err) {
		t.Fatalf("ingress path after close: %v", err)
	}
}

func mustLstat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func unixgramOwnerAvailable() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func TestUnixgramCanonicalPathUsesOnlyDarwinAlias(t *testing.T) {
	for _, path := range []string{"/tmp", "/tmp/mailbox", "/tmp-other/mailbox", "/private/tmp/mailbox"} {
		want := path
		if runtime.GOOS == "darwin" {
			switch path {
			case "/tmp":
				want = "/private/tmp"
			case "/tmp/mailbox":
				want = "/private/tmp/mailbox"
			}
		}
		if got := unixgramCanonicalPath(path); got != want {
			t.Fatalf("canonical %q on %s = %q, want %q", path, runtime.GOOS, got, want)
		}
	}
}

func TestUnixgramPathRejectsSharedParent(t *testing.T) {
	if !unixgramOwnerAvailable() {
		t.Skip("unixgram unavailable")
	}
	root, err := testfs.MkdirTemp("agw-parent-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "ingress")
	if err := validateUnixgramSocketPath(path); err != nil {
		t.Fatalf("private owner parent: %v", err)
	}
	if err := os.Chmod(root, 0750); err != nil {
		t.Fatal(err)
	}
	if err := validateUnixgramSocketPath(path); err == nil {
		t.Fatal("accepted shared parent")
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateUnixgramSocketPath(path); err != nil {
		t.Fatalf("restored private owner parent: %v", err)
	}
}
