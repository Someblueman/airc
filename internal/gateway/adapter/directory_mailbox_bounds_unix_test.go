//go:build darwin || linux

package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryMailboxQuotasAndUnsafeEntries(t *testing.T) {
	for _, kind := range []string{"inbox_quota", "results_quota", "processing_count", "unsafe_name", "oversized", "symlink", "public_subdirectory"} {
		t.Run(kind, func(t *testing.T) {
			path, m := directoryMailboxFixture(t)
			switch kind {
			case "inbox_quota", "results_quota":
				directory := "inbox"
				count := MaxDirectoryMailboxFiles + 1
				if kind == "results_quota" {
					directory = "results"
					count = MaxDirectoryMailboxFiles
				}
				for i := 0; i < count; i++ {
					if err := os.WriteFile(filepath.Join(path, directory, fmt.Sprintf("n%d.json", i)), []byte("x"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "processing_count":
				for _, name := range []string{"a.json", "b.json"} {
					if err := os.WriteFile(filepath.Join(path, "processing", name), []byte("x"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "unsafe_name":
				if err := os.WriteFile(filepath.Join(path, "inbox", "a.b.json"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(filepath.Join(path, "inbox", "a.json"), []byte(strings.Repeat("x", MaxRequestFrameBytes+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("../outside", filepath.Join(path, "inbox", "a.json")); err != nil {
					t.Fatal(err)
				}
			case "public_subdirectory":
				if err := os.Chmod(filepath.Join(path, "inbox"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := m.Claim(); err == nil {
				t.Fatal("unsafe or over-quota directory admitted", kind)
			}
		})
	}
}

func TestDirectoryMailboxExactInputAndResultBounds(t *testing.T) {
	path, m := directoryMailboxFixture(t)
	if err := os.WriteFile(filepath.Join(path, "inbox", "a.json"), []byte(strings.Repeat("x", MaxRequestFrameBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	name, body, err := m.Claim()
	if err != nil || len(body) != MaxRequestFrameBytes {
		t.Fatal("exact input boundary refused", err)
	}
	if err := m.Publish(name, []byte(strings.Repeat("r", MaxResponseFrameBytes+1))); err == nil {
		t.Fatal("oversized named result published")
	}
	if err := m.Publish("../escape.json", []byte("r")); err == nil {
		t.Fatal("result path escaped")
	}
	if err := m.Publish(name, []byte(strings.Repeat("r", MaxResponseFrameBytes))); err != nil {
		t.Fatal("exact named result boundary refused", err)
	}
}

func TestDirectoryMailboxFullResultsBlockInterruptedInput(t *testing.T) {
	path, m := directoryMailboxFixture(t)
	if err := os.WriteFile(filepath.Join(path, "processing", "pending.json"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxDirectoryMailboxFiles; i++ {
		if err := os.WriteFile(filepath.Join(path, "results", fmt.Sprintf("n%d.json", i)), []byte("r"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.Claim(); err == nil {
		t.Fatal("full results allowed resumed intake")
	}
	if err := os.Remove(filepath.Join(path, "results", "n0.json")); err != nil {
		t.Fatal(err)
	}
	name, _, err := m.Claim()
	if err != nil || name != "pending.json" {
		t.Fatal("freed result slot did not permit recovery", name, err)
	}
}
