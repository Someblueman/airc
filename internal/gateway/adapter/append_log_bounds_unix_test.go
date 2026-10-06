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

func TestAppendLogExactRecordBoundAndIncompleteOverflow(t *testing.T) {
	for _, suffix := range []string{"\n", "", "x", "x\n"} {
		t.Run(suffix, func(t *testing.T) {
			path, r := appendLogFixture(t)
			body := bytes.Repeat([]byte("x"), MaxRequestFrameBytes)
			if err := os.WriteFile(filepath.Join(path, "logs", appendLogName(1)), append(body, []byte(suffix)...), 0600); err != nil {
				t.Fatal(err)
			}
			record, err := r.Next(AppendLogCursor{Generation: 1})
			switch suffix {
			case "\n":
				if err != nil || len(record.Body) != MaxRequestFrameBytes || record.Next.Offset != MaxRequestFrameBytes+1 {
					t.Fatal(record, err)
				}
			case "":
				if !errors.Is(err, ErrAppendLogIncomplete) {
					t.Fatal("exact partial bound", err)
				}
			default:
				if err == nil || errors.Is(err, ErrAppendLogIncomplete) {
					t.Fatal("overflow tolerated", err)
				}
			}
		})
	}
}

func TestAppendLogUnsafeFilesNamesQuotaAndCursor(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public", "size", "name", "quota", "cursor", "seal_tail", "directory", "fifo", "privacy"} {
		t.Run(kind, func(t *testing.T) {
			path, r := appendLogFixture(t)
			file := filepath.Join(path, "logs", appendLogName(1))
			if err := os.WriteFile(file, []byte("body\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cursor := AppendLogCursor{Generation: 1}
			switch kind {
			case "fifo":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := unix.Mkfifo(file, 0600); err != nil {
					t.Fatal(err)
				}
			case "privacy":
				if err := os.Chmod(filepath.Join(path, "logs"), 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink", "directory":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.Mkdir(file, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(filepath.Join(path, "outside"), file); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(file, filepath.Join(path, "other")); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "size":
				if err := os.Truncate(file, MaxAppendLogBytes+1); err != nil {
					t.Fatal(err)
				}
			case "name":
				if err := os.WriteFile(filepath.Join(path, "logs", "1.jsonl"), []byte("body\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "quota":
				for i := uint64(2); i <= MaxDirectoryMailboxFiles+1; i++ {
					if err := os.WriteFile(filepath.Join(path, "logs", appendLogName(i)), []byte("body\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "cursor":
				cursor.Offset = -1
			case "seal_tail":
				if err := os.WriteFile(file, []byte("\nbody\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.Next(cursor); err == nil {
				t.Fatal("unsafe append input accepted", kind)
			}
		})
	}
}

func TestAppendLogExactGenerationSizeBound(t *testing.T) {
	path, r := appendLogFixture(t)
	file := filepath.Join(path, "logs", appendLogName(1))
	if err := os.WriteFile(file, []byte("body\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, MaxAppendLogBytes); err != nil {
		t.Fatal(err)
	}
	first, err := r.Next(AppendLogCursor{Generation: 1})
	if err != nil || string(first.Body) != "body" || first.Next.Offset != 5 {
		t.Fatal("exact generation file bound", first, err)
	}
}
