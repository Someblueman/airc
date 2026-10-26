//go:build darwin || linux

package adapter

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAppendLogMailboxExactClaimBodyAndPrivateStateBoundaries(t *testing.T) {
	body := bytes.Repeat([]byte{0xff}, MaxRequestFrameBytes)
	path, m := appendMailboxFixture(t, string(body)+"\n")
	record, err := m.Claim()
	if err != nil || !bytes.Equal(record.Body, body) {
		t.Fatal("exact opaque claim bytes changed", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = OpenAppendLogMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	record, err = m.Claim()
	if err != nil || !bytes.Equal(record.Body, body) {
		t.Fatal("base64 claim did not preserve non-UTF8 bytes after reopen", err)
	}
	if other, err := OpenAppendLogMailbox(path); err == nil {
		_ = other.Close()
		t.Fatal("second append owner allowed")
	}
}

func TestAppendLogMailboxUnsafeClaimStateRefused(t *testing.T) {
	for _, kind := range []string{"public", "hardlink", "symlink", "fifo", "oversized", "unknown_field", "noncanonical_base64"} {
		t.Run(kind, func(t *testing.T) {
			path, m := appendMailboxFixture(t, "body\n")
			if _, err := m.Claim(); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(path, "claim.json")
			switch kind {
			case "public":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(file, filepath.Join(path, "other")); err != nil {
					t.Fatal(err)
				}
			case "symlink", "fifo":
				if err := os.Rename(file, filepath.Join(path, "old")); err != nil {
					t.Fatal(err)
				}
				if kind == "fifo" {
					if err := unix.Mkfifo(file, 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(filepath.Join(path, "old"), file); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Truncate(file, maxAppendClaimBytes+1); err != nil {
					t.Fatal(err)
				}
			case "unknown_field":
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				data = append([]byte(`{"extra":1,`), data[1:]...)
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "noncanonical_base64":
				claim, err := m.claim()
				if err != nil {
					t.Fatal(err)
				}
				claim.Body += "\n"
				data, err := json.Marshal(claim)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Claim(); err == nil {
				t.Fatal("unsafe retained state admitted", kind)
			}
		})
	}
}
