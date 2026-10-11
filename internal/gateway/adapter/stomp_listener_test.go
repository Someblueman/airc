//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPListenerPasswordConnectionsAndReuse(t *testing.T) {
	slots := make(chan struct{}, 2)
	_, dial, token, cancel, done := stompListenerFixture(t, slots)
	first := dial()
	r, f := stompClientConnect(t, first, token)
	if f.Command != "CONNECTED" {
		t.Fatal(f)
	}
	// A wrong passcode gets an ERROR frame, then the connection closes.
	second := dial()
	_, f = stompClientConnect(t, second, "wrong")
	if message, _ := f.Header("message"); f.Command != "ERROR" || message == "" {
		t.Fatal("wrong passcode got a session", f)
	}
	waitAMQPSlots(t, slots, 1)
	stompClientWrite(t, first, stompwire.Frame{Command: "DISCONNECT", Headers: []stompwire.Header{{Key: "receipt", Value: "bye"}}})
	f, err := stompwire.Read(r)
	receipt, _ := f.Header("receipt-id")
	if err != nil || f.Command != "RECEIPT" || receipt != "bye" {
		t.Fatal(f, err)
	}
	if _, err := stompwire.Read(r); err == nil {
		t.Fatal("frame after final receipt")
	}
	waitAMQPSlots(t, slots, 0)
	third := dial()
	_, f = stompClientConnect(t, third, token)
	if f.Command != "CONNECTED" {
		t.Fatal("slot not reusable", f)
	}
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Fatal("shutdown leaked slots")
	}
}

// TestSTOMPListenerFailurePathsReleaseOwners covers STOMP's own session
// endings; TestTLSListenerCarriers covers accept and read failures.
func TestSTOMPListenerFailurePathsReleaseOwners(t *testing.T) {
	for _, mode := range []string{"unsupported", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			slots := make(chan struct{}, 1)
			_, dial, token, cancel, done := stompListenerFixture(t, slots)
			client := dial()
			r, f := stompClientConnect(t, client, token)
			if f.Command != "CONNECTED" {
				t.Fatal(f)
			}
			switch mode {
			case "disconnect":
				stompClientWrite(t, client, stompwire.Frame{Command: "DISCONNECT"})
				if _, err := stompwire.Read(r); err == nil {
					t.Fatal("DISCONNECT did not close")
				}
			case "unsupported":
				stompClientWrite(t, client, stompwire.Frame{Command: "SEND", Body: []byte("unverified")})
				f, err := stompwire.Read(r)
				if err != nil || f.Command != "ERROR" {
					t.Fatal(f, err)
				}
				if _, err := stompwire.Read(r); err == nil {
					t.Fatal("frame after ERROR")
				}
			}
			waitAMQPSlots(t, slots, 0)
			cancel()
			if err := amqpListenerFinished(t, done); err != nil {
				t.Fatal(err)
			}
		})
	}
}
