//go:build (darwin || linux) && arm64

package gateway_test

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nativeQueueFixtureBytes = 16 << 10

// provisionSharedMemory gives the test fixture, and only the fixture, ownership
// of one kernel object plus its private transaction directory. lockRoot is the
// canonical 0700 parent supplied by the caller.
func provisionSharedMemory(t *testing.T, lockRoot string) (name, directory string) {
	t.Helper()
	if !filepath.IsAbs(lockRoot) || filepath.Clean(lockRoot) != lockRoot {
		t.Fatalf("shared-memory lock root is not canonical: %q", lockRoot)
	}
	resolved, err := filepath.EvalSymlinks(lockRoot)
	if err != nil || resolved != lockRoot {
		t.Fatalf("shared-memory lock root resolves elsewhere: %q -> %q: %v", lockRoot, resolved, err)
	}
	rootInfo, err := os.Stat(lockRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0700 {
		t.Fatalf("shared-memory lock root must be an owner-only directory: %q mode=%v err=%v", lockRoot, rootInfo, err)
	}

	var random [11]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name = "/agw-shm-" + hex.EncodeToString(random[:])
	if len(name) != 31 {
		t.Fatalf("shared-memory name length=%d want 31", len(name))
	}
	directory = filepath.Join(lockRoot, strings.TrimPrefix(name, "/"))
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatalf("create per-name shared-memory directory: %v", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatalf("set per-name shared-memory directory mode: %v", err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode().Perm() != 0700 {
		t.Fatalf("per-name shared-memory directory must be owner-only: mode=%v err=%v", directoryInfo, err)
	}
	lockPath := filepath.Join(directory, "transaction.lock")
	objectOwned := false
	var removeObject func() error
	t.Cleanup(func() {
		if objectOwned {
			if err := removeObject(); err != nil {
				t.Errorf("remove owned shared-memory object %s: %v", name, err)
			}
		}
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove fixture transaction lock %s: %v", lockPath, err)
		}
		if err := os.Remove(directory); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove per-name shared-memory directory %s: %v", directory, err)
		}
	})
	lock, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatalf("create fixture transaction.lock: %v", err)
	}
	if err := lock.Chmod(0600); err != nil {
		_ = lock.Close()
		t.Fatalf("set fixture transaction.lock mode: %v", err)
	}
	lockInfo, statErr := lock.Stat()
	closeErr := lock.Close()
	if statErr != nil || closeErr != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0600 {
		t.Fatalf("fixture transaction.lock must be a regular 0600 file: mode=%v stat=%v close=%v", lockInfo, statErr, closeErr)
	}
	removeObject, err = provisionSharedMemoryObject(t, name)
	if err != nil {
		t.Fatalf("provision native shared-memory object %s: %v", name, err)
	}
	objectOwned = true
	return name, directory
}

func validSharedMemoryFixtureName(name string) bool {
	if len(name) != 31 || !strings.HasPrefix(name, "/agw-shm-") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(name, "/agw-shm-"))
	return err == nil
}
