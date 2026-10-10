//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPListenerSharedSlotsAndJoinedShutdown(t *testing.T) {
	slots := make(chan struct{}, 2)
	slots <- struct{}{}
	_, dial, _, cancel, done := stompListenerFixture(t, slots, 1)
	first := dial()
	waitAMQPSlots(t, slots, 2)
	second := dial()
	if err := second.HandshakeContext(t.Context()); err == nil {
		t.Fatal("overcapacity TLS admitted")
	}
	if len(slots) != 2 {
		t.Fatal("refusal changed foreign slots")
	}
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatal("shutdown leaked or stole slots")
	}
	var b [1]byte
	if _, err := first.Read(b[:]); err == nil {
		t.Fatal("preauthentication socket remains live")
	}
	<-slots
}

func TestSTOMPListenerPrincipalAdmissionBeforeConnectedAndReuse(t *testing.T) {
	slots := make(chan struct{}, 2)
	_, dial, token, cancel, done := stompListenerFixture(t, slots, 1)
	first := dial()
	r, f := stompClientConnect(t, first, token)
	if f.Command != "CONNECTED" {
		t.Fatal(f)
	}
	second := dial()
	_, f = stompClientConnect(t, second, token)
	if f.Command != "ERROR" {
		t.Fatal("capacity got successful session", f)
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
		t.Fatal("principal slot not reusable", f)
	}
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Fatal("principal shutdown leaked slots")
	}
}

func TestSTOMPListenerFailurePathsReleaseOwners(t *testing.T) {
	for _, mode := range []string{"accept", "read", "unsupported", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			slots := make(chan struct{}, 1)
			l, dial, token, cancel, done := stompListenerFixture(t, slots, 1)
			client := dial()
			if mode == "accept" {
				waitAMQPSlots(t, slots, 1)
				_ = l.Close()
				if err := amqpListenerFinished(t, done); !errors.Is(err, net.ErrClosed) {
					t.Fatal(err)
				}
			} else {
				r, f := stompClientConnect(t, client, token)
				if f.Command != "CONNECTED" {
					t.Fatal(f)
				}
				switch mode {
				case "read":
					closeMQTTConnection(client)
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
			}
			if len(slots) != 0 {
				t.Fatal("owner not joined")
			}
		})
	}
}
