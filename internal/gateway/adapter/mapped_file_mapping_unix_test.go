//go:build (darwin && arm64) || (linux && arm64)

package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func mappedFileFixture(t *testing.T) (*os.Root, string) {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("resolve test directory:", err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal("chmod test directory:", err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal("open test root:", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error("close test root:", err)
		}
	})
	return root, path
}

func provisionMappedFile(t *testing.T, root *os.Root, mode os.FileMode, size int64) {
	t.Helper()
	file, err := root.OpenFile(mappedFileQueueName, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
	if err != nil {
		t.Fatal("create test queue file:", err)
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		t.Fatal("chmod test queue file:", err)
	}
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		t.Fatal("size test queue file:", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal("close test queue file:", err)
	}
}

func TestMappedFileMappingReadsWritesAndClose(t *testing.T) {
	root, _ := mappedFileFixture(t)
	provisionMappedFile(t, root, 0600, mappedFileQueueBytes)
	mapping, err := openMappedFileMapping(root)
	if err != nil {
		t.Fatal("open mapped queue:", err)
	}
	if len(mapping.Bytes()) != mappedFileQueueBytes {
		t.Fatal("mapped queue size", len(mapping.Bytes()))
	}
	if err := mapping.Validate(); err != nil {
		t.Fatal("validate mapped queue:", err)
	}
	copy(mapping.Bytes()[128:], []byte("mapped-file-check"))
	file, err := root.OpenFile(mappedFileQueueName, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal("open independent reader:", err)
	}
	got := make([]byte, len("mapped-file-check"))
	if _, err := file.ReadAt(got, 128); err != nil {
		_ = file.Close()
		t.Fatal("read mapped bytes through independent descriptor:", err)
	}
	_ = file.Close()
	if string(got) != "mapped-file-check" {
		t.Fatalf("independent descriptor read %q", got)
	}
	if err := mapping.Close(); err != nil {
		t.Fatal("close mapped queue:", err)
	}
	if err := mapping.Close(); err != nil {
		t.Fatal("close mapped queue twice:", err)
	}
	if mapping.Bytes() != nil {
		t.Fatal("closed mapping returned bytes")
	}
	if err := mapping.Validate(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed mapping validate error=%v, want os.ErrClosed", err)
	}
}

func TestMappedFileMappingRejectsBadMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		mode os.FileMode
		size int64
	}{
		{name: "mode", mode: 0640, size: mappedFileQueueBytes},
		{name: "size", mode: 0600, size: mappedFileQueueBytes / 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := mappedFileFixture(t)
			provisionMappedFile(t, root, test.mode, test.size)
			if mapping, err := openMappedFileMapping(root); !errors.Is(err, ErrMappedQueueInvalid) {
				if mapping != nil {
					_ = mapping.Close()
				}
				t.Fatalf("invalid file accepted: mapping=%v err=%v", mapping, err)
			}
		})
	}

	t.Run("hardlink", func(t *testing.T) {
		root, path := mappedFileFixture(t)
		provisionMappedFile(t, root, 0600, mappedFileQueueBytes)
		if err := os.Link(filepath.Join(path, mappedFileQueueName), filepath.Join(path, "alias")); err != nil {
			t.Skipf("filesystem does not permit hardlinks in fixture: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(filepath.Join(path, "alias")) })
		if mapping, err := openMappedFileMapping(root); !errors.Is(err, ErrMappedQueueInvalid) {
			if mapping != nil {
				_ = mapping.Close()
			}
			t.Fatalf("hard-linked file accepted: mapping=%v err=%v", mapping, err)
		}
	})
}

func TestMappedFileMappingRejectsSymlinkAndFIFO(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root, path := mappedFileFixture(t)
		target := filepath.Join(path, "target")
		if err := os.WriteFile(target, make([]byte, mappedFileQueueBytes), 0600); err != nil {
			t.Fatal("create symlink target:", err)
		}
		if err := os.Symlink(target, filepath.Join(path, mappedFileQueueName)); err != nil {
			t.Fatal("create symlink:", err)
		}
		if mapping, err := openMappedFileMapping(root); !errors.Is(err, ErrMappedQueueInvalid) {
			if mapping != nil {
				_ = mapping.Close()
			}
			t.Fatalf("symlink accepted: mapping=%v err=%v", mapping, err)
		}
	})

	t.Run("fifo", func(t *testing.T) {
		root, path := mappedFileFixture(t)
		if err := unix.Mkfifo(filepath.Join(path, mappedFileQueueName), 0600); err != nil {
			t.Fatal("create FIFO:", err)
		}
		if mapping, err := openMappedFileMapping(root); !errors.Is(err, ErrMappedQueueInvalid) {
			if mapping != nil {
				_ = mapping.Close()
			}
			t.Fatalf("FIFO accepted: mapping=%v err=%v", mapping, err)
		}
	})
}

func TestMappedFileMappingValidateRejectsReplacement(t *testing.T) {
	root, path := mappedFileFixture(t)
	provisionMappedFile(t, root, 0600, mappedFileQueueBytes)
	mapping, err := openMappedFileMapping(root)
	if err != nil {
		t.Fatal("open mapped queue:", err)
	}
	defer mapping.Close()
	if err := root.Rename(mappedFileQueueName, "queue.old"); err != nil {
		t.Fatal("rename mapped file:", err)
	}
	provisionMappedFile(t, root, 0600, mappedFileQueueBytes)
	if err := mapping.Validate(); !errors.Is(err, ErrMappedQueueInvalid) {
		t.Fatalf("replaced path validation error=%v, want invalid", err)
	}
	if err := os.Remove(filepath.Join(path, "queue.old")); err != nil {
		t.Fatal("remove old test file:", err)
	}
}
