//go:build (darwin || linux) && arm64

package gateway_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func runMappedFileCrashWriter(directory, mode string, stage *os.File) (retErr error) {
	if mode != "writing-partial" && mode != "ready" && mode != "writing-reset" {
		return errors.New("unknown mapped-file crash stage")
	}
	lock, err := os.OpenFile(filepath.Join(directory, "transaction.lock"), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	queue, err := os.OpenFile(filepath.Join(directory, "queue.dat"), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, queue.Close()) }()
	info, err := queue.Stat()
	if err != nil || info.Size() != nativeQueueFixtureBytes || !info.Mode().IsRegular() {
		return errors.New("mapped-file crash queue has invalid type or size")
	}
	view, err := unix.Mmap(int(queue.Fd()), 0, nativeQueueFixtureBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, unix.Munmap(view)) }()
	if len(view) != nativeQueueFixtureBytes || string(view[4:8]) != "AGQ1" || atomic.LoadUint32((*uint32)(unsafe.Pointer(&view[0]))) != 0 {
		return errors.New("mapped-file crash slot did not start EMPTY with valid static header")
	}
	state := (*uint32)(unsafe.Pointer(&view[0]))
	var body []byte
	switch mode {
	case "writing-partial":
		body = []byte("partial")
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], uint32(len(body)))
		copy(view[16:], body)
	case "ready":
		body = []byte("complete mapped-file record before writer death")
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], uint32(len(body)))
		copy(view[16:], body)
		atomic.StoreUint32(state, 2)
	case "writing-reset":
		body = []byte("consumer copied before reset")
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], uint32(len(body)))
		copy(view[16:], body)
		atomic.StoreUint32(state, 2)
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], 0)
	}
	witness := []byte(mode + "\n")
	n, err := stage.Write(witness)
	if err != nil || n != len(witness) {
		return errors.Join(err, syscall.EIO)
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	<-timer.C
	return errors.New("mapped-file crash child hold timed out before parent kill")
}
