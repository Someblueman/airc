//go:build !linux || (!arm64 && !amd64)

package adapter

import (
	"context"
	"errors"
	"testing"
)

func TestPOSIXQueueUnsupported(t *testing.T) {
	for _, open := range []func(string, int) (*POSIXQueue, error){CreatePOSIXQueue, OpenPOSIXQueue} {
		if q, err := open("/queue", 2); q != nil || !errors.Is(err, ErrPOSIXQueueUnsupported) {
			t.Fatal("unsupported queue accepted", q, err)
		}
	}
	q := &POSIXQueue{}
	if err := q.WriteMessage(context.Background(), []byte("body")); !errors.Is(err, ErrPOSIXQueueUnsupported) {
		t.Fatal(err)
	}
	if _, _, err := q.ReadMessage(context.Background(), make([]byte, POSIXQueueMessageBytes)); !errors.Is(err, ErrPOSIXQueueUnsupported) {
		t.Fatal(err)
	}
}
