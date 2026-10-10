//go:build (darwin || linux) && arm64

package adapter

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"net"
	"os"
	"testing"
	"time"
)

func fillSystemVResponse(t *testing.T, response *sysvqueue.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	for {
		err := response.WriteMessage(ctx, make([]byte, 1000))
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestSystemVQueuePacketsMutableDeadlinesAndJoinedClose(t *testing.T) {
	conn, _, response := systemVPacketsFixture(t)
	read := make(chan error, 1)
	go func() { _, _, err := conn.ReadFrom(make([]byte, 1000)); read <- err }()
	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-read:
		var ne net.Error
		if !errors.Is(err, os.ErrDeadlineExceeded) || !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not wake read")
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	fillSystemVResponse(t, response)
	write := make(chan error, 1)
	go func() {
		_, err := conn.WriteTo(make([]byte, 1000), &SystemVQueueAddr{Key: response.Identity().Key})
		write <- err
	}()
	go func() { _, _, err := conn.ReadFrom(make([]byte, 1000)); read <- err }()
	waitQueuePacketOperations(t, conn, 2)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{read, write} {
		select {
		case err := <-done:
			if !errors.Is(err, net.ErrClosed) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("close failed to join")
		}
	}
	if err := conn.SetDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestSystemVQueuePacketsExtendWriteDeadline(t *testing.T) {
	conn, _, response := systemVPacketsFixture(t)
	fillSystemVResponse(t, response)
	if err := conn.SetWriteDeadline(time.Now().Add(120 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := conn.WriteTo(make([]byte, 1000), &SystemVQueueAddr{Key: response.Identity().Key})
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatal("old deadline retained", err)
	default:
	}
	if _, err := response.ReadMessage(t.Context(), make([]byte, 1000)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not resume")
	}
}

// Wait for actual registered I/O, so Close is exercised with both users blocked.
func waitQueuePacketOperations(t *testing.T, conn *queuePackets, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn.mu.Lock()
		ready := len(conn.operations) == count
		conn.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("queue users did not enter I/O")
}
