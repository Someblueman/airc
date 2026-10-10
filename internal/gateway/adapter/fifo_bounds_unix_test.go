//go:build darwin || linux

package adapter

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"testing"
	"time"
)

func TestFIFOAbsentWriterHardBootstrapBound(t *testing.T) {
	f, _, _ := fifoFixture(t)
	if err := f.SetReadDeadline(time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.Read(make([]byte, 1)); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("absence: %v", err)
		}
	case <-time.After(6 * time.Second):
		_ = f.Close()
		<-done
		t.Fatal("bootstrap exceeded hard bound")
	}
}

func TestFIFOChangedDeadlineAndCloseInterruptBootstrap(t *testing.T) {
	for _, action := range []string{"deadline", "close"} {
		t.Run(action, func(t *testing.T) {
			f, _, _ := fifoFixture(t)
			done := make(chan error, 1)
			go func() { _, err := f.Read(make([]byte, 1)); done <- err }()
			if action == "close" {
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err := f.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				want := os.ErrClosed
				if action == "deadline" {
					want = os.ErrDeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("%s: %v", action, err)
				}
			case <-time.After(time.Second):
				_ = f.Close()
				<-done
				t.Fatal("bootstrap did not join")
			}
		})
	}
}

func TestFIFOConnectedEmptyDeadlineAndBackpressure(t *testing.T) {
	f, root, reader := fifoFixture(t)
	writer := fifoWriter(t, root)
	if err := f.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("connected empty read: %v", err)
	}
	if err := f.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if n, err := f.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Fatalf("read after deadline: %d %v", n, err)
	}
	if err := f.SetWriteDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if n, err := f.Write(make([]byte, 4<<20)); n == 4<<20 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("backpressure: %d %v", n, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, unix.EPIPE) {
		t.Fatalf("closed return peer: %v", err)
	}
}

func TestFIFOCloseInterruptsBlockedReturnWrite(t *testing.T) {
	f, _, reader := fifoFixture(t)
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.Write(make([]byte, 4<<20)); done <- err }()
	// Seeing a byte proves the write reached the actual FIFO. Reading one byte
	// cannot drain this oversized write, so Close must interrupt its backpressure.
	if n, err := reader.Read(make([]byte, 1)); n != 1 || err != nil {
		_ = f.Close()
		<-done
		t.Fatalf("write did not reach pipe: %d %v", n, err)
	}
	select {
	case err := <-done:
		t.Fatalf("oversized write finished before close: %v", err)
	default:
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("closed blocked write: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked writer did not join")
	}
}
