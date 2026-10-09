//go:build linux && arm64

package sharedmem

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

var mappingTestID atomic.Uint64

func provisionObject(t *testing.T, size int, mode uint32) (string, *os.File) {
	t.Helper()
	name := fmt.Sprintf("/agwtest-%d-%d", os.Getpid(), mappingTestID.Add(1))
	path := filepath.Join("/dev/shm", name[1:])
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC, mode)
	if err != nil {
		t.Fatal("create test shared-memory object:", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		t.Fatal("wrap test object descriptor")
	}
	if err := file.Truncate(int64(size)); err != nil {
		_ = file.Close()
		t.Fatal("size test object:", err)
	}
	if err := file.Chmod(os.FileMode(mode)); err != nil {
		_ = file.Close()
		t.Fatal("set test object mode:", err)
	}
	t.Cleanup(func() {
		_ = file.Close()
		_ = os.Remove(path)
	})
	return name, file
}

func TestMappingCrossDescriptorIOAndIdempotentClose(t *testing.T) {
	name, producer := provisionObject(t, MaxMappingBytes, 0600)
	before := []byte("producer-before-map")
	if _, err := producer.WriteAt(before, 0); err != nil {
		t.Fatal("write initial producer bytes:", err)
	}

	m, err := OpenExisting(name, MaxMappingBytes)
	if err != nil {
		t.Fatal("open provisioned shared memory:", err)
	}
	want := append([]byte(nil), before...)
	if !bytes.Equal(m.Bytes()[:len(want)], want) {
		t.Fatal("mapping did not observe producer write")
	}
	mappedWrite := []byte("gateway-mapped-write")
	copy(m.Bytes()[64:], mappedWrite)
	got := make([]byte, len(mappedWrite))
	if _, err := producer.ReadAt(got, 64); err != nil {
		t.Fatal("read mapped bytes through independent descriptor:", err)
	}
	if !bytes.Equal(got, mappedWrite) {
		t.Fatalf("independent descriptor read %q, want %q", got, mappedWrite)
	}
	if err := m.Close(); err != nil {
		t.Fatal("close mapping:", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal("second close:", err)
	}
	if m.Bytes() != nil {
		t.Fatal("closed mapping returned bytes")
	}
}

func TestMappingRejectsModeAndSizeMismatch(t *testing.T) {
	wrongMode, _ := provisionObject(t, MaxMappingBytes, 0644)
	if m, err := OpenExisting(wrongMode, MaxMappingBytes); !errors.Is(err, ErrInvalid) {
		if m != nil {
			_ = m.Close()
		}
		t.Fatalf("mode mismatch accepted: mapping=%v err=%v", m, err)
	}

	wrongSize, _ := provisionObject(t, MaxMappingBytes/2, 0600)
	if m, err := OpenExisting(wrongSize, MaxMappingBytes); !errors.Is(err, ErrInvalid) {
		if m != nil {
			_ = m.Close()
		}
		t.Fatalf("size mismatch accepted: mapping=%v err=%v", m, err)
	}
}

func TestMappingRejectsSymlinkAndFIFOWithoutBlocking(t *testing.T) {
	target, _ := provisionObject(t, MaxMappingBytes, 0600)
	link := fmt.Sprintf("/agwtest-%d-%d", os.Getpid(), mappingTestID.Add(1))
	linkPath := filepath.Join("/dev/shm", link[1:])
	if err := os.Symlink(filepath.Join("/dev/shm", target[1:]), linkPath); err != nil {
		t.Fatal("create symlink fixture:", err)
	}
	t.Cleanup(func() { _ = os.Remove(linkPath) })
	if m, err := OpenExisting(link, MaxMappingBytes); !errors.Is(err, ErrInvalid) {
		if m != nil {
			_ = m.Close()
		}
		t.Fatalf("symlink accepted: mapping=%v err=%v", m, err)
	}

	fifo := fmt.Sprintf("/agwtest-%d-%d", os.Getpid(), mappingTestID.Add(1))
	fifoPath := filepath.Join("/dev/shm", fifo[1:])
	if err := unix.Mkfifo(fifoPath, 0600); err != nil {
		t.Fatal("create FIFO fixture:", err)
	}
	t.Cleanup(func() { _ = os.Remove(fifoPath) })
	if m, err := OpenExisting(fifo, MaxMappingBytes); !errors.Is(err, ErrInvalid) {
		if m != nil {
			_ = m.Close()
		}
		t.Fatalf("FIFO accepted: mapping=%v err=%v", m, err)
	}
}

func TestMappingRemainsUsableAfterNameUnlink(t *testing.T) {
	name, _ := provisionObject(t, MaxMappingBytes, 0600)
	m, err := OpenExisting(name, MaxMappingBytes)
	if err != nil {
		t.Fatal("open provisioned shared memory:", err)
	}
	if err := unix.Unlink(filepath.Join("/dev/shm", name[1:])); err != nil {
		_ = m.Close()
		t.Fatal("unlink test name:", err)
	}
	copy(m.Bytes()[:8], []byte("stillmap"))
	if got := string(m.Bytes()[:8]); got != "stillmap" {
		t.Fatalf("mapping after unlink = %q", got)
	}
	if err := m.Close(); err != nil {
		t.Fatal("close retained mapping:", err)
	}
}

func TestMappingValidateDetectsModeAndSizeChanges(t *testing.T) {
	t.Run("mode", func(t *testing.T) {
		name, file := provisionObject(t, MaxMappingBytes, 0600)
		m, err := OpenExisting(name, MaxMappingBytes)
		if err != nil {
			t.Fatal("open mapping:", err)
		}
		defer m.Close()
		if err := file.Chmod(0640); err != nil {
			t.Fatal("change object mode:", err)
		}
		if err := m.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("changed mode validation error = %v, want ErrInvalid", err)
		}
	})

	t.Run("size", func(t *testing.T) {
		name, file := provisionObject(t, MaxMappingBytes, 0600)
		m, err := OpenExisting(name, MaxMappingBytes)
		if err != nil {
			t.Fatal("open mapping:", err)
		}
		defer m.Close()
		if err := file.Truncate(MaxMappingBytes / 2); err != nil {
			t.Fatal("change object size:", err)
		}
		if err := m.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("changed size validation error = %v, want ErrInvalid", err)
		}
	})
}

func TestMappingValidateDetectsHardlinkAlias(t *testing.T) {
	name, _ := provisionObject(t, MaxMappingBytes, 0600)
	m, err := OpenExisting(name, MaxMappingBytes)
	if err != nil {
		t.Fatal("open mapping:", err)
	}
	defer m.Close()
	alias := fmt.Sprintf("/agwtest-%d-%d", os.Getpid(), mappingTestID.Add(1))
	aliasPath := filepath.Join("/dev/shm", alias[1:])
	if err := os.Link(filepath.Join("/dev/shm", name[1:]), aliasPath); err != nil {
		t.Fatal("create hardlink alias:", err)
	}
	t.Cleanup(func() { _ = os.Remove(aliasPath) })
	if err := m.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hardlink alias validation error = %v, want ErrInvalid", err)
	}
	if err := os.Remove(aliasPath); err != nil {
		t.Fatal("remove hardlink alias:", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal("validation after alias removal:", err)
	}
}

func TestMappingValidateAfterClose(t *testing.T) {
	name, _ := provisionObject(t, MaxMappingBytes, 0600)
	m, err := OpenExisting(name, MaxMappingBytes)
	if err != nil {
		t.Fatal("open mapping:", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal("close mapping:", err)
	}
	if err := m.Validate(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed mapping validation error = %v, want os.ErrClosed", err)
	}
	var nilMapping *Mapping
	if err := nilMapping.Validate(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("nil mapping validation error = %v, want os.ErrClosed", err)
	}
}
