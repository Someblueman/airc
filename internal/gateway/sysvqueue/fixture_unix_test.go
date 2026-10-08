//go:build (darwin || linux) && arm64

package sysvqueue

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/unix"
	"testing"
)

func fixture(t *testing.T) *Queue {
	t.Helper()
	for range 16 {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			t.Fatal(err)
		}
		key := int32(binary.LittleEndian.Uint32(raw[:]) & 0x7fffffff)
		if key == 0 {
			continue
		}
		q, err := Create(key)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if q.closed {
				return
			}
			if err := q.Close(); err != nil {
				t.Error(err)
			}
		})
		return q
	}
	t.Fatal("unable to create exclusive queue")
	return nil
}
