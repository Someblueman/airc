//go:build darwin || linux

package adapter

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

// tlsListenerCarrier serves a carrier over TLS under shared socket slots.
type tlsListenerCarrier struct {
	name string
	// start serves the carrier; session opens an authenticated session.
	start func(t *testing.T, slots chan struct{}) (l net.Listener, session func() *tls.Conn, done <-chan error, cancel func())
}

func tlsListenerCarriers() []tlsListenerCarrier {
	return []tlsListenerCarrier{
		{
			name: "amqp",
			start: func(t *testing.T, slots chan struct{}) (net.Listener, func() *tls.Conn, <-chan error, func()) {
				l, dial, token, cancel, done := amqpListenerFixture(t, slots)
				return l, func() *tls.Conn {
					c := dial()
					if err := c.HandshakeContext(t.Context()); err != nil {
						t.Fatal(err)
					}
					amqpClientNegotiate(t, c, token)
					return c
				}, done, cancel
			},
		},
		{
			name: "stomp",
			start: func(t *testing.T, slots chan struct{}) (net.Listener, func() *tls.Conn, <-chan error, func()) {
				l, dial, token, cancel, done := stompListenerFixture(t, slots)
				return l, func() *tls.Conn {
					c := dial()
					if _, f := stompClientConnect(t, c, token); f.Command != "CONNECTED" {
						t.Fatal(f)
					}
					return c
				}, done, cancel
			},
		},
		{
			name: "syslog-tls",
			start: func(t *testing.T, slots chan struct{}) (net.Listener, func() *tls.Conn, <-chan error, func()) {
				_, l, dial, cancel, done := syslogListenerWithSlots(t, slots)
				return l, dial, done, cancel
			},
		},
	}
}

// TestTLSListenerCarriers exercises the shared socket slots of the TLS
// listeners once per carrier: an incomplete TLS peer holds a slot, a peer
// over capacity is closed without touching another transport's slot,
// shutdown joins every owner, a closed listener ends the owner, and a
// session that fails releases its slot.
func TestTLSListenerCarriers(t *testing.T) {
	for _, c := range tlsListenerCarriers() {
		t.Run(c.name, func(t *testing.T) {
			t.Run("shared-slots-and-joined-shutdown", func(t *testing.T) {
				slots := make(chan struct{}, 2)
				slots <- struct{}{} // A different transport already owns one shared slot.
				l, _, done, cancel := c.start(t, slots)
				first := rawDial(t, l)
				waitAMQPSlots(t, slots, 2)
				second := rawDial(t, l)
				if !closedByPeer(second) {
					t.Fatal("over-capacity peer admitted or retained")
				}
				if len(slots) != 2 {
					t.Fatal("refusal changed foreign capacity")
				}
				cancel()
				if err := amqpListenerFinished(t, done); err != nil {
					t.Fatal(err)
				}
				if len(slots) != 1 {
					t.Fatal("shutdown leaked or stole a shared slot")
				}
				if !closedByPeer(first) {
					t.Fatal("incomplete TLS negotiation remains live")
				}
			})
			t.Run("accept-failure", func(t *testing.T) {
				slots := make(chan struct{}, 1)
				l, _, done, _ := c.start(t, slots)
				rawDial(t, l)
				waitAMQPSlots(t, slots, 1)
				_ = l.Close()
				if err := amqpListenerFinished(t, done); !errors.Is(err, net.ErrClosed) {
					t.Fatal(err)
				}
				if len(slots) != 0 {
					t.Fatal("connection owner not joined")
				}
			})
			t.Run("read-failure-releases-owner", func(t *testing.T) {
				slots := make(chan struct{}, 1)
				_, session, done, cancel := c.start(t, slots)
				closeMQTTConnection(session())
				waitAMQPSlots(t, slots, 0)
				cancel()
				if err := amqpListenerFinished(t, done); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func rawDial(t *testing.T, l net.Listener) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", l.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// closedByPeer reports whether the server closed c, rather than leaving it
// to time out.
func closedByPeer(c net.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	_, err := c.Read(make([]byte, 1))
	return err != nil && !isTimeout(err)
}
