//go:build darwin && arm64

package sysvqueue

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestCloseJoinsBlockedQueueUsers(t *testing.T) {
	for _, writing := range []bool{false, true} {
		t.Run(map[bool]string{false: "reader", true: "writer"}[writing], func(t *testing.T) {
			q := fixture(t)
			if writing {
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
				for q.WriteMessage(ctx, make([]byte, MessageBytes)) == nil {
				}
				cancel()
			}
			entered := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				close(entered)
				if writing {
					result <- q.WriteMessage(t.Context(), make([]byte, MessageBytes))
				} else {
					_, err := q.ReadMessage(t.Context(), make([]byte, MessageBytes))
					result <- err
				}
			}()
			<-entered
			if err := q.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, net.ErrClosed) {
					t.Fatal("closed queue user", err)
				}
			case <-time.After(time.Second):
				t.Fatal("queue user did not join")
			}
		})
	}
}
