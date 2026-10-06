//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOfflineBundleFilesAreReadOnlyAndNeverExecuted(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bundle.json")
	// Executable mode and shell syntax in an opaque field grant no execution.
	marker := filepath.Join(root, "marker")
	token := "$(touch " + marker + ")"
	data, err := json.Marshal(OfflineBundle{Version: 1, Envelopes: []string{token}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOfflineBundle(path)
	if err != nil || got.Envelopes[0] != token {
		t.Fatal("read opaque envelope", got, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("modified input", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("executed supplied input", err)
	}
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(root, "oversize")
	if err := os.WriteFile(large, []byte(strings.Repeat(" ", MaxOfflineBundleBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{link, pipe, root, large, "relative.json", path + "/../bundle.json"} {
		if _, err := ReadOfflineBundle(unsafe); err == nil {
			t.Fatalf("accepted unsafe input %q", unsafe)
		}
	}
}
