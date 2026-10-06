//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func appendLogFixture(t *testing.T) (string, *AppendLogReader) {
	t.Helper()
	path := mailboxDirectory(t)
	if err := os.Mkdir(filepath.Join(path, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := OpenAppendLogReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return path, r
}

func TestAppendLogCursorPartialSealAndGenerationRecovery(t *testing.T) {
	path, r := appendLogFixture(t)
	file := filepath.Join(path, "logs", appendLogName(1))
	if err := os.WriteFile(file, []byte("first\npart"), 0600); err != nil {
		t.Fatal(err)
	}
	cursor := AppendLogCursor{Generation: 1}
	first, err := r.Next(cursor)
	if err != nil || string(first.Body) != "first" || first.Next.Offset != 6 || first.Next.Sealed {
		t.Fatal(first, err)
	}
	// A proposal can be serialized by the future owner, but the reader itself
	// does not publish or checkpoint it.
	encoded, err := json.Marshal(first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &cursor); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(cursor); !errors.Is(err, ErrAppendLogIncomplete) {
		t.Fatal("partial record advanced", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = OpenAppendLogReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString("ial\n\n")
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		t.Fatal(err)
	}
	second, err := r.Next(cursor)
	if err != nil || string(second.Body) != "partial" || second.Next.Offset != 14 {
		t.Fatal(second, err)
	}
	seal, err := r.Next(second.Next)
	if err != nil || !seal.Next.Sealed || len(seal.Body) != 0 || seal.Next.Offset != 15 {
		t.Fatal(seal, err)
	}
	if _, err := r.Next(seal.Next); !errors.Is(err, io.EOF) {
		t.Fatal("absent next generation not EOF", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "logs", appendLogName(2)), []byte("next\n"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := r.Next(seal.Next)
	if err != nil || string(next.Body) != "next" || next.Next.Generation != 2 || next.Next.Offset != 5 {
		t.Fatal(next, err)
	}
}

func TestAppendLogRefusesReplacementTruncationAndMissingUnsealedGeneration(t *testing.T) {
	for _, kind := range []string{"replacement", "truncation", "missing", "after_seal", "gap"} {
		t.Run(kind, func(t *testing.T) {
			path, r := appendLogFixture(t)
			file := filepath.Join(path, "logs", appendLogName(1))
			if err := os.WriteFile(file, []byte("first\n\n"), 0600); err != nil {
				t.Fatal(err)
			}
			first, err := r.Next(AppendLogCursor{Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			cursor := first.Next
			switch kind {
			case "replacement":
				temporary := filepath.Join(path, "replacement")
				if err := os.WriteFile(temporary, []byte("first\n\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(temporary, file); err != nil {
					t.Fatal(err)
				}
			case "truncation":
				if err := os.Truncate(file, 2); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "after_seal", "gap":
				seal, err := r.Next(cursor)
				if err != nil {
					t.Fatal(err)
				}
				cursor = seal.Next
				if kind == "gap" {
					if err := os.WriteFile(filepath.Join(path, "logs", appendLogName(3)), []byte("later\n"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(file, []byte("first\n\nlater\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := r.Next(cursor); err == nil || errors.Is(err, io.EOF) {
				t.Fatal("unsafe generation accepted", kind, err)
			}
		})
	}
}
