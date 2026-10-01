package pathcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDistinctRejectsFileAndParentAliases(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "key")
	os.WriteFile(original, []byte("private"), 0600)
	link := filepath.Join(dir, "hard")
	if err := os.Link(original, link); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "sym")
	if err := os.Symlink(original, symlink); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(dir, parent); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{original, link}, {original, symlink}, {filepath.Join(dir, "future"), filepath.Join(parent, "future")}} {
		if err := Distinct(pair[0], pair[1]); err == nil {
			t.Fatal("accepted alias", pair)
		}
	}
	if err := Distinct(original, "", filepath.Join(dir, "history")); err != nil {
		t.Fatal(err)
	}
}
