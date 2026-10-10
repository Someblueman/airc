//go:build (darwin || linux) && arm64

package gateway_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// provisionMappedFileQueue creates the ordinary-file endpoint before the
// gateway starts. Production only opens this directory and its two files.
func provisionMappedFileQueue(t *testing.T, root string) string {
	t.Helper()
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatalf("mapped-file fixture root is not canonical: %q", root)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		t.Fatalf("mapped-file fixture root resolves elsewhere: %q -> %q: %v", root, resolved, err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0700 {
		t.Fatalf("mapped-file fixture root must be owner-only: %q mode=%v err=%v", root, rootInfo, err)
	}

	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "mapped-"+hex.EncodeToString(random[:]))
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatalf("create mapped-file queue directory: %v", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatalf("set mapped-file queue directory mode: %v", err)
	}
	t.Cleanup(func() {
		for _, name := range []string{"queue.dat", "transaction.lock"} {
			if err := os.Remove(filepath.Join(directory, name)); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove mapped-file fixture %s: %v", name, err)
			}
		}
		if err := os.Remove(directory); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove mapped-file queue directory: %v", err)
		}
	})

	queue := createMappedFixtureFile(t, directory, "queue.dat")
	if err := queue.Truncate(nativeQueueFixtureBytes); err != nil {
		_ = queue.Close()
		t.Fatalf("size mapped-file queue: %v", err)
	}
	header := []byte("AGQ1")
	n, err := queue.WriteAt(header, 4)
	if err != nil || n != len(header) {
		_ = queue.Close()
		t.Fatalf("initialize mapped-file queue header: wrote %d/%d: %v", n, len(header), err)
	}
	verifyMappedFixtureFile(t, queue, nativeQueueFixtureBytes)
	if err := queue.Close(); err != nil {
		t.Fatalf("close mapped-file queue after provisioning: %v", err)
	}
	lock := createMappedFixtureFile(t, directory, "transaction.lock")
	verifyMappedFixtureFile(t, lock, 0)
	if err := lock.Close(); err != nil {
		t.Fatalf("close mapped-file transaction lock after provisioning: %v", err)
	}
	return directory
}

func createMappedFixtureFile(t *testing.T, directory, name string) *os.File {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatalf("create mapped-file fixture %s: %v", name, err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		t.Fatalf("set mapped-file fixture %s mode: %v", name, err)
	}
	return file
}

func verifyMappedFixtureFile(t *testing.T, file *os.File, size int64) {
	t.Helper()
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat mapped-file fixture: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != size || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		t.Fatalf("mapped-file fixture ownership/mode/size/link count invalid: info=%v stat=%v err=%v", info, stat, err)
	}
}

func mappedFileFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(fmt.Errorf("resolve mapped-file fixture root: %w", err))
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
