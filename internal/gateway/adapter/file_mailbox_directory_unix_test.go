//go:build darwin || linux

package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileMailboxRequiresPrivateDirectory(t *testing.T) {
	path := mailboxDirectory(t)
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if m, err := OpenFileMailbox(path); err == nil {
		_ = m.Close()
		t.Fatal("public directory opened")
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(mailboxDirectory(t), "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if m, err := OpenFileMailbox(alias); err == nil {
		_ = m.Close()
		t.Fatal("symlink directory opened")
	}
	m, err := OpenFileMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Claim(); err == nil {
		t.Fatal("claim after directory became public")
	}
	if err := m.Publish([]byte("result")); err == nil {
		t.Fatal("publish after directory became public")
	}
}
