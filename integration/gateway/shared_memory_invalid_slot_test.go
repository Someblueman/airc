//go:build (darwin || linux) && arm64

package gateway_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/sharedmem"
)

func TestSharedMemoryInvalidSlotFailsWithoutRepair(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"magic", "reserved", "state", "ready-zero", "ready-oversize", "empty-length"} {
		t.Run(kind, func(t *testing.T) {
			name, directory := provisionSharedMemory(t, root)
			mapping, err := sharedmem.OpenExisting(name, sharedMemoryFixtureBytes)
			if err != nil {
				t.Fatal(err)
			}
			defer mapping.Close()
			view := mapping.Bytes()
			// No queue participant is open while this invalid object is prepared.
			switch kind {
			case "magic":
				view[4] = 'X'
			case "reserved":
				view[12] = 1
			case "state":
				atomic.StoreUint32((*uint32)(unsafe.Pointer(&view[0])), 3)
			case "ready-zero":
				atomic.StoreUint32((*uint32)(unsafe.Pointer(&view[0])), 2)
			case "ready-oversize":
				binary.LittleEndian.PutUint32(view[8:12], 1001)
				atomic.StoreUint32((*uint32)(unsafe.Pointer(&view[0])), 2)
			case "empty-length":
				binary.LittleEndian.PutUint32(view[8:12], 1)
			}
			before := append([]byte(nil), view...)
			for _, consumer := range []bool{true, false} {
				queue, err := adapter.OpenSharedMemoryQueue(name, directory, consumer)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				if consumer {
					_, err = queue.ReadMessage(ctx, make([]byte, 1000))
				} else {
					err = queue.WriteMessage(ctx, []byte("must not repair"))
				}
				cancel()
				if closeErr := queue.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if !errors.Is(err, adapter.ErrSharedMemoryInvalid) || !bytes.Equal(view, before) {
					t.Fatal("invalid slot was accepted or modified", consumer, err)
				}
			}
		})
	}
}
