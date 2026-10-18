//go:build linux && (arm64 || amd64)

package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testmqueue"
)

func newTestPOSIXQueue(t *testing.T, depth int) *POSIXQueue {
	t.Helper()
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	q, err := CreatePOSIXQueue(fmt.Sprintf("/agw-test-%x", nonce[:]), depth)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return q
}

func TestPOSIXQueueWholeMessageAndBounds(t *testing.T) {
	testmqueue.Require(t)
	q := newTestPOSIXQueue(t, 2)
	producer, err := OpenPOSIXQueue(q.name, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	if producer.Identity() != q.Identity() {
		t.Fatal("opened different queue inode")
	}
	body := bytes.Repeat([]byte{0xa5}, POSIXQueueMessageBytes)
	if err := producer.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.ReadMessage(t.Context(), make([]byte, POSIXQueueMessageBytes-1)); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal("short receive not refused", err)
	}
	output := make([]byte, POSIXQueueMessageBytes)
	n, priority, err := q.ReadMessage(t.Context(), output)
	if err != nil || n != len(body) || priority != 0 || !bytes.Equal(body, output) {
		t.Fatal("whole message mismatch", n, priority, err)
	}
	for _, invalid := range [][]byte{nil, {}, make([]byte, POSIXQueueMessageBytes+1)} {
		if err := producer.WriteMessage(t.Context(), invalid); !errors.Is(err, ErrPOSIXQueueInvalid) {
			t.Fatal("invalid message accepted", err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := q.ReadMessage(ctx, output); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("invalid writes reached queue", err)
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenPOSIXQueue(q.name, 2)
	if err != nil {
		t.Fatal("producer close unlinked owned queue", err)
	}
	_ = reopened.Close()
}

func TestPOSIXQueueFullPressureDeadlineAndDrain(t *testing.T) {
	testmqueue.Require(t)
	q := newTestPOSIXQueue(t, 1)
	if err := q.WriteMessage(t.Context(), []byte("first")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := q.WriteMessage(ctx, []byte("second")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("full queue did not stop", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatal("pressure wait exceeded bounded deadline", elapsed)
	}
	out := make([]byte, POSIXQueueMessageBytes)
	n, _, err := q.ReadMessage(t.Context(), out)
	if err != nil || string(out[:n]) != "first" {
		t.Fatal("pressure changed first message", n, err)
	}
	if err := q.WriteMessage(t.Context(), []byte("after-drain")); err != nil {
		t.Fatal(err)
	}
	n, _, err = q.ReadMessage(t.Context(), out)
	if err != nil || string(out[:n]) != "after-drain" {
		t.Fatal("queue did not recover after drain", n, err)
	}
}

func TestPOSIXQueueCloseJoinsWaitingReadAndWrite(t *testing.T) {
	testmqueue.Require(t)
	for _, write := range []bool{false, true} {
		t.Run(fmt.Sprint(write), func(t *testing.T) {
			q := newTestPOSIXQueue(t, 1)
			if write {
				if err := q.WriteMessage(t.Context(), []byte("full")); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() {
				if write {
					done <- q.WriteMessage(context.Background(), []byte("waiting"))
				} else {
					_, _, err := q.ReadMessage(context.Background(), make([]byte, POSIXQueueMessageBytes))
					done <- err
				}
			}()
			time.Sleep(50 * time.Millisecond)
			if err := q.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, net.ErrClosed) {
					t.Fatal("closed operation result", err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("closed operation did not join")
			}
			if err := q.Close(); err != nil {
				t.Fatal("repeated close", err)
			}
		})
	}
}
