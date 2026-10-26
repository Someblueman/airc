//go:build linux

package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixPacketForeignUIDGate(t *testing.T) {
	testUnixListenerRejectsForeignUID(t, "unixpacket", ListenUnixPacket)
}

func TestUnixPacketPrivatePathLifecycleAndReplacement(t *testing.T) {
	path := unixPacketPath(t)
	listener, err := ListenUnixPacket(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	initial, err := os.Lstat(path)
	if err != nil || initial.Mode()&os.ModeSocket == 0 || initial.Mode().Perm() != 0600 {
		t.Fatal("record socket was not owner-only", initial, err)
	}
	if other, err := ListenUnixPacket(path); err == nil || other != nil {
		t.Fatal("replaced active record listener", other, err)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(initial, current) {
		t.Fatal("duplicate changed record socket", err)
	}
	// Retain the old inode, so the replacement cannot reuse its inode number.
	retained := path + "-old"
	if err := os.Rename(path, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	replacement, err := os.Lstat(path)
	if err != nil || os.SameFile(initial, replacement) {
		t.Fatal("replacement did not have a distinct inode", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" {
		t.Fatal("close removed replacement", string(data), err)
	}
	clean := filepath.Join(filepath.Dir(path), "clean")
	listener, err = ListenUnixPacket(clean)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(clean); !os.IsNotExist(err) {
		t.Fatal("owned record socket survived close", err)
	}
}

func TestUnixPacketRefusesUnsafeAndOverlongPaths(t *testing.T) {
	path := unixPacketPath(t)
	root := filepath.Dir(path)
	existing := filepath.Join(root, "existing")
	if err := os.WriteFile(existing, []byte("owned file"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative.sock", root + "/../socket", existing, filepath.Join(link, "socket"), root + "/" + strings.Repeat("a", 108-len(root)-1)} {
		if listener, err := ListenUnixPacket(bad); err == nil || listener != nil {
			t.Fatal("accepted unsafe record path", bad, listener, err)
		}
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "owned file" {
		t.Fatal("changed existing file", string(data), err)
	}
	exact := root + "/" + strings.Repeat("a", 107-len(root)-1)
	listener, err := ListenUnixPacket(exact)
	if err != nil {
		t.Fatal("refused exact path bound", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if listener, err := ListenUnixPacket(path); err == nil || listener != nil {
		t.Fatal("accepted non-private parent", listener, err)
	}
}
