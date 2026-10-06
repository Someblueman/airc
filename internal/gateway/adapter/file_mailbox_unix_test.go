//go:build darwin || linux

package adapter

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func mailboxDirectory(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFileMailboxClaimRestartAndPublish(t *testing.T) {
	path := mailboxDirectory(t)
	request := bytes.Repeat([]byte("q"), MaxRequestFrameBytes)
	if err := os.WriteFile(filepath.Join(path, "request.json"), request, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := OpenFileMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := OpenFileMailbox(path); err == nil {
		_ = other.Close()
		t.Fatal("second owner admitted")
	}
	got, err := m.Claim()
	if err != nil || !bytes.Equal(got, request) {
		t.Fatal("claim mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(path, "request.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("input not atomically claimed", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = OpenFileMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got, err = m.Claim()
	if err != nil || !bytes.Equal(got, request) {
		t.Fatal("claimed input lost after restart", err)
	}
	if err := m.Publish(bytes.Repeat([]byte("r"), MaxResponseFrameBytes+1)); err == nil {
		t.Fatal("oversized result published")
	}
	if _, err := m.Claim(); err != nil {
		t.Fatal("failed publication lost input", err)
	}
	response := bytes.Repeat([]byte("r"), MaxResponseFrameBytes)
	if err := m.Publish(response); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(path, "response.json"))
	if err != nil || !bytes.Equal(got, response) {
		t.Fatal("result mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(path, "processing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("processing file retained after result", err)
	}
	if _, err := m.Claim(); !errors.Is(err, ErrMailboxResultPending) {
		t.Fatal("unread result did not block intake", err)
	}
}

func TestFileMailboxRejectsUnsafeInputs(t *testing.T) {
	for _, kind := range []string{"empty", "oversized", "public", "symlink", "hardlink", "fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path := mailboxDirectory(t)
			input := filepath.Join(path, "request.json")
			var err error
			switch kind {
			case "empty":
				err = os.WriteFile(input, nil, 0600)
			case "oversized":
				err = os.WriteFile(input, bytes.Repeat([]byte("x"), MaxRequestFrameBytes+1), 0600)
			case "public":
				err = os.WriteFile(input, []byte("x"), 0644)
			case "symlink":
				err = os.Symlink("outside", input)
			case "hardlink":
				if err = os.WriteFile(filepath.Join(path, "other"), []byte("x"), 0600); err == nil {
					err = os.Link(filepath.Join(path, "other"), input)
				}
			case "fifo":
				err = unix.Mkfifo(input, 0600)
			case "directory":
				err = os.Mkdir(input, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			m, err := OpenFileMailbox(path)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if _, err := m.Claim(); err == nil {
				t.Fatal("unsafe input claimed")
			}
			if _, err := os.Lstat(input); err != nil {
				t.Fatal("refusal changed producer input", err)
			}
		})
	}
}
