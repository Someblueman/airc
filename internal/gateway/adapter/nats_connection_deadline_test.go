//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSExpiredDeadlinePreservesBufferedFrame(t *testing.T) {
	client, c, _ := natsConnectionFixture(t, false)
	written := make(chan error, 1)
	go func() { _, err := client.Write([]byte("PING\r\n")); written <- err }()
	if _, err := c.reader.Peek(6); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if _, err := c.readBefore(time.Now().Add(-time.Millisecond)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired buffered read: %v", err)
	}
	if c.reader.Buffered() != 6 {
		t.Fatal("expired read consumed pipeline")
	}
	if f, err := c.readBefore(time.Now().Add(time.Second)); err != nil || f.Command != "PING" {
		t.Fatalf("retained frame: %v", err)
	}
}

func TestNATSStartedFrameAbsoluteDeadline(t *testing.T) {
	client, c, _ := natsConnectionFixture(t, false)
	written := make(chan struct{})
	go func() {
		defer close(written)
		for _, b := range []byte("SUB airc.response 1\r\n") {
			if _, err := client.Write([]byte{b}); err != nil {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()
	started := time.Now()
	if _, err := c.readBefore(time.Time{}); err == nil {
		t.Fatal("trickled frame accepted")
	}
	if elapsed := time.Since(started); elapsed < natsDeadline-time.Second || elapsed > natsDeadline+time.Second {
		t.Fatalf("frame deadline changed: %v", elapsed)
	}
	c.close()
	closeMQTTConnection(client)
	<-written
}

func TestNATSCallerDeadlineBoundsHeartbeatsAndWrites(t *testing.T) {
	for _, direction := range []string{"read", "write"} {
		t.Run(direction, func(t *testing.T) {
			client, c, _ := natsConnectionFixture(t, false)
			deadline := time.Now().Add(time.Second)
			written := make(chan struct{})
			if direction == "read" {
				go func() {
					defer close(written)
					for {
						if _, err := client.Write([]byte("PING\r\n")); err != nil {
							return
						}
						time.Sleep(20 * time.Millisecond)
					}
				}()
			} else {
				close(written)
			}
			if direction == "read" {
				for {
					f, err := c.readBefore(deadline)
					if err != nil {
						break
					}
					if f.Command != "PING" {
						t.Fatal("unexpected frame")
					}
				}
			} else {
				if err := c.writeBefore(natswire.Frame{Command: "PONG"}, deadline, false); err == nil {
					t.Fatal("blocked write succeeded")
				}
			}
			if time.Now().After(deadline.Add(time.Second)) {
				t.Fatal("caller deadline renewed")
			}
			c.close()
			closeMQTTConnection(client)
			<-written
		})
	}
}
