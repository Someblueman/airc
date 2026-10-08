//go:build (darwin || linux) && arm64

package adapter

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"golang.org/x/sys/unix"
	"testing"
)

func systemVQueueFixture(t *testing.T) *sysvqueue.Queue {
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
		q, err := sysvqueue.Create(key)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := q.Close(); err != nil {
				t.Error(err)
			}
		})
		return q
	}
	t.Fatal("unable to create queue")
	return nil
}
func systemVPacketsFixture(t *testing.T) (*queuePackets, *sysvqueue.Queue, *sysvqueue.Queue) {
	t.Helper()
	ingress, response := systemVQueueFixture(t), systemVQueueFixture(t)
	producer, err := sysvqueue.Open(ingress.Identity().Key)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := sysvqueue.Open(response.Identity().Key)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := NewSystemVQueuePackets(ingress, map[int32]*sysvqueue.Queue{response.Identity().Key: writer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		_ = producer.Close()
	})
	return conn, producer, response
}
