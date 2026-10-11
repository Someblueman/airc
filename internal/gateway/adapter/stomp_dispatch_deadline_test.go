//go:build darwin || linux

package adapter

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPDispatchPendingDeadlineCannotBeRenewedByHeartbeats(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done, _ := stompDispatchFixture(t, slots)
	client := dial()
	r, connected := stompClientConnect(t, client, token)
	if connected.Command != "CONNECTED" {
		t.Fatal(connected)
	}
	if err := client.SetDeadline(time.Now().Add(40 * time.Second)); err != nil {
		t.Fatal(err)
	}
	stompClientWrite(t, client, stompSubscribeFrame())
	stompReadFrame(t, r, "RECEIPT")
	stompClientWrite(t, client, stompRequestFrame(t, amqpRequest()))
	stompReadFrame(t, r, "RECEIPT")
	stompReadFrame(t, r, "MESSAGE")
	start := time.Now()
	stop, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if _, err := client.Write([]byte("\n")); err != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() { close(stop); closeMQTTConnection(client); <-joined })
	if f, err := stompwire.Read(r); err == nil {
		t.Fatal("frame after expired pending result", f)
	}
	elapsed := time.Since(start)
	if elapsed < 28*time.Second || elapsed > 35*time.Second {
		t.Fatal("pending deadline", elapsed)
	}
	waitAMQPSlots(t, slots, 0)
	cancel()
	amqpListenerFinished(t, done)
}
