package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

const maxLogBytes = 5 << 20

// rotatingLog bounds long-lived service logs without a background goroutine.
type rotatingLog struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
	// closed distinguishes Close from a failed rotation, which is retried.
	closed bool
}

func openLog(path string) (*rotatingLog, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("log path must be a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		err = f.Chmod(0600)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &rotatingLog{path: path, file: f, size: info.Size()}, nil
}

func (w *rotatingLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > maxLogBytes {
		return 0, fmt.Errorf("log record exceeds %d bytes", maxLogBytes)
	}
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.file != nil && w.size+int64(len(p)) > maxLogBytes {
		_ = w.file.Close()
		w.file = nil
		// If the backup cannot be made, keep appending for another window
		// rather than lose every later record.
		_ = os.Rename(w.path, w.path+".1")
	}
	if w.file == nil {
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return 0, err // retried on the next record
		}
		w.file, w.size = f, 0
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
