//go:build (darwin || linux) && arm64

package gateway_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Someblueman/airc/internal/gateway/sharedmem"
	"golang.org/x/sys/unix"
)

func runSharedMemoryCrashWriter(name, directory, mode string, stage io.Writer) (retErr error) {
	if mode != "writing-partial" && mode != "ready" && mode != "writing-reset" {
		return fmt.Errorf("unknown crash stage %q", mode)
	}
	lock, err := os.OpenFile(directory+string(os.PathSeparator)+"transaction.lock", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open crash-test transaction lock: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock crash-test transaction: %w", err)
	}
	mapping, err := sharedmem.OpenExisting(name, nativeQueueFixtureBytes)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, mapping.Close()) }()
	view := mapping.Bytes()
	if len(view) != nativeQueueFixtureBytes || uintptr(unsafe.Pointer(&view[0]))%unsafe.Alignof(uint32(0)) != 0 || string(view[4:8]) != "AGQ1" {
		return errors.New("crash-test mapping has invalid shape or static header")
	}
	state := (*uint32)(unsafe.Pointer(&view[0]))
	if atomicState := atomic.LoadUint32(state); atomicState != 0 {
		return fmt.Errorf("crash-test slot did not start EMPTY: state=%d", atomicState)
	}
	var wantStage string
	switch mode {
	case "writing-partial":
		wantStage = mode
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], 37)
		copy(view[16:24], []byte("partial!"))
	case "ready":
		wantStage = mode
		body := []byte("complete record published before writer death")
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], uint32(len(body)))
		copy(view[16:16+len(body)], body)
		atomic.StoreUint32(state, 2)
	case "writing-reset":
		wantStage = mode
		body := []byte("consumer copied before reset")
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], uint32(len(body)))
		copy(view[16:16+len(body)], body)
		atomic.StoreUint32(state, 2)
		// Model consumer resetSlot after its local copy, then die while WRITING.
		atomic.StoreUint32(state, 1)
		binary.LittleEndian.PutUint32(view[8:12], 0)
	}
	witness := []byte(wantStage + "\n")
	n, err := stage.Write(witness)
	if err != nil || n != len(witness) {
		return fmt.Errorf("write crash-test stage witness: wrote %d/%d: %w", n, len(witness), errors.Join(err, io.ErrShortWrite))
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	<-timer.C
	return errors.New("crash-test child hold timed out before parent kill")
}
