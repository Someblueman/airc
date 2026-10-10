package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesWithPermissionsInEveryMode(t *testing.T) {
	for _, mode := range []Sync{SyncFull, SyncFlush, SyncNone} {
		path := filepath.Join(t.TempDir(), "state.json")
		for _, content := range []string{"first", "second"} {
			if err := Write(path, []byte(content), 0o600, mode); err != nil {
				t.Fatal(mode, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != content {
				t.Fatalf("mode %d: %q %v", mode, got, err)
			}
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %d: permissions %v %v", mode, info.Mode(), err)
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		if len(entries) != 1 {
			t.Fatalf("mode %d left temporary files: %v", mode, entries)
		}
	}
}

func TestWriteFailureLeavesTheOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "state.json")
	if err := Write(path, []byte("x"), 0o600, SyncFlush); err == nil {
		t.Fatal("write into a missing directory succeeded")
	}
	kept := filepath.Join(dir, "kept")
	if err := os.WriteFile(kept, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad, err := SetAside(kept)
	if err != nil || bad != kept+".bad" {
		t.Fatal(bad, err)
	}
	if got, _ := os.ReadFile(bad); string(got) != "old" {
		t.Fatalf("set-aside copy: %q", got)
	}
}

func TestParseSync(t *testing.T) {
	for name, want := range map[string]Sync{"full": SyncFull, "fsync": SyncFlush, "none": SyncNone} {
		if got, err := ParseSync(name); err != nil || got != want {
			t.Fatal(name, got, err)
		}
	}
	if _, err := ParseSync("fast"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestReadBoundedRefusesMissingAndOversizedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if _, err := ReadBounded(path, 4); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBounded(path, 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized file error = %v", err)
	}
	if data, err := ReadBounded(path, 5); err != nil || string(data) != "12345" {
		t.Fatalf("file at the limit: %q %v", data, err)
	}
}
