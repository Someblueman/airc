// Package atomicfile replaces state files without ever exposing a partial
// write, with one durability policy shared by the daemon and the CLI.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Sync says how hard a write tries to reach stable storage.
type Sync int

const (
	// SyncFull asks the device to flush its cache (F_FULLFSYNC on macOS) and
	// syncs the directory entry: the write survives power loss.
	SyncFull Sync = iota
	// SyncFlush hands the data to the device with fsync(2): the write survives
	// a process or OS crash. On Linux this is the same as SyncFull without the
	// directory sync; on macOS it is far cheaper because the drive cache is
	// not flushed.
	SyncFlush
	// SyncNone leaves flushing to the OS: the write survives a process crash.
	SyncNone
)

func ParseSync(name string) (Sync, error) {
	switch name {
	case "full":
		return SyncFull, nil
	case "fsync":
		return SyncFlush, nil
	case "none":
		return SyncNone, nil
	}
	return SyncFull, fmt.Errorf("sync mode must be full, fsync or none, not %q", name)
}

// File flushes an open file according to mode.
func File(f *os.File, mode Sync) error {
	switch mode {
	case SyncFull:
		return f.Sync()
	case SyncFlush:
		for {
			err := syscall.Fsync(int(f.Fd()))
			if !errors.Is(err, syscall.EINTR) {
				return err
			}
		}
	}
	return nil
}

// Write replaces path with data: temp file in the same directory, flush,
// rename, and with SyncFull a directory sync so the rename itself is durable.
func Write(path string, data []byte, perm os.FileMode, mode Sync) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Chmod(perm)
	}
	if err == nil {
		err = File(temp, mode)
	}
	if err = errors.Join(err, temp.Close()); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	if mode != SyncFull {
		return nil
	}
	return Dir(dir)
}

// Dir makes renames and creations inside dir durable.
func Dir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// SetAside moves an unreadable state file out of the way so its owner can
// start empty instead of failing forever, and returns where it went.
func SetAside(path string) (string, error) {
	bad := path + ".bad"
	return bad, os.Rename(path, bad)
}
