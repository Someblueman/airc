//go:build darwin || linux

package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/store"
)

func TestAppendLogMailboxSealSkipsDispatch(t *testing.T) {
	f, _ := frameFixture(t)
	path, m := appendMailboxFixture(t, "\n")
	if err := ProcessAppendLogMailbox(t.Context(), m, f.d, nil); err != nil {
		t.Fatal(err)
	}
	cursor, err := m.cursor()
	if err != nil || !cursor.Sealed || cursor.Offset != 1 {
		t.Fatal("seal not checkpointed", cursor, err)
	}
	if _, err := os.Stat(filepath.Join(path, "response.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("seal published response", err)
	}
	if _, err := f.lookup(t, "frame-message"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("seal reached journal", err)
	}
}
