//go:build linux && (arm64 || amd64)

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testmqueue"
)

func TestPOSIXQueuePacketsExtendWriteDeadline(t *testing.T) {
	testmqueue.Require(t)
	conn, _, response := queuePacketFixture(t)
	if err := response.WriteMessage(t.Context(), []byte("full")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(80 * time.Millisecond))
	done := make(chan error, 1)
	go func() { _, err := conn.WriteTo([]byte("next"), &POSIXQueueAddr{Name: response.Name()}); done <- err }()
	time.Sleep(40 * time.Millisecond)
	_ = conn.SetWriteDeadline(time.Time{})
	time.Sleep(80 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("old deadline retained: %v", err)
	default:
	}
	buffer := make([]byte, POSIXQueueMessageBytes)
	if _, _, err := response.ReadMessage(t.Context(), buffer); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("writer did not resume")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	n, _, err := response.ReadMessage(ctx, buffer)
	if err != nil || string(buffer[:n]) != "next" {
		t.Fatal("next message", err)
	}
}
