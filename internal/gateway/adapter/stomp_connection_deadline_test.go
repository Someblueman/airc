//go:build darwin || linux

package adapter

import (
	"testing"
	"time"
)

func TestSTOMPConnectionReadDeadlines(t *testing.T) {
	for _, mode := range []string{"partial", "trickle", "idle", "operation-heartbeats"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			heartbeat := "0,0"
			if mode == "idle" {
				heartbeat = "1000,0"
			}
			client, c, _ := stompConnectionFixture(t, heartbeat)
			deadline := time.Time{}
			budget := 4 * time.Second
			if mode == "operation-heartbeats" {
				deadline = time.Now().Add(250 * time.Millisecond)
				budget = time.Second
			}
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				for {
					_, err := c.readBefore(deadline)
					if err != nil {
						done <- err
						return
					}
				}
			}()
			t.Cleanup(func() { c.close(); <-finished })
			written := make(chan struct{})
			go func() {
				defer close(written)
				if mode == "idle" {
					return
				}
				if mode == "partial" {
					_, _ = client.Write([]byte("SEND\nx:"))
					return
				}
				payload := []byte("a")
				interval := 50 * time.Millisecond
				if mode == "operation-heartbeats" {
					payload = []byte{'\n'}
					interval = 20 * time.Millisecond
				} else {
					if _, err := client.Write([]byte("SEND\nx:")); err != nil {
						return
					}
				}
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for range ticker.C {
					if _, err := client.Write(payload); err != nil {
						return
					}
				}
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("deadline accepted")
				}
			case <-time.After(budget):
				c.close()
				<-finished
				t.Fatal("read deadline extended")
			}
			c.close()
			select {
			case <-written:
			case <-time.After(time.Second):
				t.Fatal("deadline writer did not join")
			}
		})
	}
}
