//go:build (darwin || linux) && arm64

package gateway_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Someblueman/airc/internal/gateway/sharedmem"
	"golang.org/x/sys/unix"
)

type nativeQueueView interface {
	Bytes() []byte
	Close() error
}

type mappedFileQueueView struct {
	file *os.File
	view []byte
}

func (m *mappedFileQueueView) Bytes() []byte { return m.view }
func (m *mappedFileQueueView) Close() error {
	if m == nil {
		return nil
	}
	var err error
	if m.view != nil {
		err = unix.Munmap(m.view)
		m.view = nil
	}
	if m.file != nil {
		if closeErr := m.file.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		m.file = nil
	}
	return err
}

func openNativeQueueView(kind, address string) (nativeQueueView, error) {
	if kind == "shared_memory" {
		return sharedmem.OpenExisting(address, nativeQueueFixtureBytes)
	}
	if kind != "mapped_file" {
		return nil, fmt.Errorf("unknown native queue kind %q", kind)
	}
	file, err := os.OpenFile(filepath.Join(address, "queue.dat"), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	view, err := unix.Mmap(int(file.Fd()), 0, nativeQueueFixtureBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &mappedFileQueueView{file: file, view: view}, nil
}
