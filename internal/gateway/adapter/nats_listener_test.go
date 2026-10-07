//go:build darwin || linux

package adapter

import (
	"bufio"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSListenerSharedSlotsAndJoinedShutdown(t *testing.T) {
	slots := make(chan struct{}, 2)
	slots <- struct{}{}
	_, dial, _, cancel, done := natsListenerFixture(t, slots, 1)
	first := dial()
	waitAMQPSlots(t, slots, 2)
	// The admitted socket is stalled before INFO/TLS; it already owns capacity.
	second := dial()
	if _, err := natswire.Read(bufio.NewReader(second.NetConn())); err == nil {
		t.Fatal("overcapacity received INFO")
	}
	if len(slots) != 2 {
		t.Fatal("refusal stole foreign capacity")
	}
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatal("shutdown leaked or stole capacity")
	}
	var b [1]byte
	// An already-buffered public INFO may precede EOF on a real TCP socket.
	for {
		if _, err := first.NetConn().Read(b[:]); err != nil {
			break
		}
	}
	<-slots
}

func TestNATSListenerPrincipalAdmissionBeforeAcknowledgementAndReuse(t *testing.T) {
	slots := make(chan struct{}, 2)
	_, dial, token, cancel, done := natsListenerFixture(t, slots, 1)
	first := dial()
	r, f := natsClientConnect(t, first, token)
	if f.Command != "+OK" {
		t.Fatal("first principal refused")
	}
	second := dial()
	_, f = natsClientConnect(t, second, token)
	if f.Command != "-ERR" {
		t.Fatal("principal overcapacity acknowledged")
	}
	waitAMQPSlots(t, slots, 1)
	if _, err := first.Write([]byte("SUB airc.response 1\r\nPING\r\n")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+OK", "PONG"} {
		if f, err := natswire.Read(r); err != nil || f.Command != want {
			t.Fatalf("control %s: %v", want, err)
		}
	}
	closeMQTTConnection(first)
	waitAMQPSlots(t, slots, 0)
	third := dial()
	_, f = natsClientConnect(t, third, token)
	if f.Command != "+OK" {
		t.Fatal("principal capacity not reusable")
	}
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Fatal("shutdown leaked capacity")
	}
}

func TestNATSListenerFailurePathsReleaseOwners(t *testing.T) {
	for _, mode := range []string{"accept", "credential", "read", "malformed", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			slots := make(chan struct{}, 1)
			l, dial, token, cancel, done := natsListenerFixture(t, slots, 1)
			client := dial()
			if mode == "accept" {
				waitAMQPSlots(t, slots, 1)
				_ = l.Close()
				if err := amqpListenerFinished(t, done); !errors.Is(err, net.ErrClosed) {
					t.Fatal(err)
				}
			} else {
				if mode == "credential" {
					if _, err := natswire.Read(bufio.NewReader(client.NetConn())); err != nil {
						t.Fatal(err)
					}
					if err := client.HandshakeContext(t.Context()); err != nil {
						t.Fatal(err)
					}
					if _, err := client.Write(natsConnectBytes(strings.Repeat("0", 64))); err != nil {
						t.Fatal(err)
					}
					var b [1]byte
					if _, err := client.Read(b[:]); err == nil {
						t.Fatal("bad credential socket open")
					}
				} else {
					r, f := natsClientConnect(t, client, token)
					if f.Command != "+OK" {
						t.Fatal("principal refused")
					}
					switch mode {
					case "read":
						closeMQTTConnection(client)
					case "malformed":
						if _, err := client.Write([]byte("PUB a 32769\r\n")); err != nil {
							t.Fatal(err)
						}
						if _, err := natswire.Read(r); err == nil {
							t.Fatal("oversized frame retained socket")
						}
					case "unsupported":
						if _, err := client.Write([]byte("PUB airc.request airc.response 1\r\nx\r\n")); err != nil {
							t.Fatal(err)
						}
						if f, err := natswire.Read(r); err != nil || f.Command != "-ERR" {
							t.Fatal("unimplemented publication acknowledged", err)
						}
						if _, err := natswire.Read(r); err == nil {
							t.Fatal("frame after terminal")
						}
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
