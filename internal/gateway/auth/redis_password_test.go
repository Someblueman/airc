//go:build unix

package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRedisPasswordExactBytesAndBounds(t *testing.T) {
	root := privateTemp(t)
	path := filepath.Join(root, "password")
	for _, content := range [][]byte{[]byte("x"), []byte(" arbitrary password\r\n\x00é "), bytes.Repeat([]byte("p"), MaxRedisPassword)} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadRedisPassword(path)
		if err != nil || !bytes.Equal(got, content) {
			t.Fatal("password bytes changed or refused", err)
		}
		got[0] ^= 0xff
		again, err := ReadRedisPassword(path)
		if err != nil || !bytes.Equal(again, content) {
			t.Fatal("returned buffer aliases another read", err)
		}
		clear(got)
		clear(again)
		if _, err := ReadToken(path); !errors.Is(err, ErrCredential) {
			t.Fatal("authorSpec credential format changed")
		}
	}
	for _, size := range []int{0, MaxRedisPassword + 1, MaxRedisPassword * 4} {
		if err := os.WriteFile(path, bytes.Repeat([]byte("p"), size), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadRedisPassword(path)
		if got != nil || !errors.Is(err, ErrCredential) {
			t.Fatal("accepted password outside bounds")
		}
	}
}

func TestRedisPasswordPrivateFileBoundary(t *testing.T) {
	root := privateTemp(t)
	path := filepath.Join(root, "password")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	refuse := func(path string) {
		t.Helper()
		got, err := ReadRedisPassword(path)
		if got != nil || !errors.Is(err, ErrCredential) || err.Error() != ErrCredential.Error() {
			t.Fatal("unsafe credential accepted or disclosed", err)
		}
	}
	refuse("relative")
	refuse(filepath.Join(root, "missing"))
	refuse(root)
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	refuse(link)
	parent := filepath.Join(root, "parent-link")
	if err := os.Symlink(root, parent); err != nil {
		t.Fatal(err)
	}
	refuse(filepath.Join(parent, "password"))
	fifo := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	refuse(fifo)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	refuse(path)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	refuse(path)
}
