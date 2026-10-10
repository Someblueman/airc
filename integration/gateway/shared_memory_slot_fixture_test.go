//go:build (darwin || linux) && arm64

package gateway_test

import (
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func sharedMemorySlotState(t *testing.T, mapping nativeQueueView) uint32 {
	t.Helper()
	view := mapping.Bytes()
	if len(view) != nativeQueueFixtureBytes || uintptr(unsafe.Pointer(&view[0]))%unsafe.Alignof(uint32(0)) != 0 || string(view[4:8]) != "AGQ1" || binary.LittleEndian.Uint32(view[12:16]) != 0 {
		t.Fatal("invalid native queue mapping while observing slot")
	}
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(&view[0])))
}

func waitSharedMemorySlotState(t *testing.T, mapping nativeQueueView, want uint32, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(2 * time.Millisecond)
	defer poll.Stop()
	for {
		if got := sharedMemorySlotState(t, mapping); got == want {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("test context ended waiting for native queue slot state %d: %v", want, t.Context().Err())
		case <-deadline.C:
			t.Fatalf("native queue slot did not reach state %d", want)
		case <-poll.C:
		}
	}
}
