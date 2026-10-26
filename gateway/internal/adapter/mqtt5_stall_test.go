package adapter

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/mqttwire"
)

// A follow that waits for acknowledgements a client never sends ends the
// connection after mqtt5AckWait, so the follow returns and frees the
// gateway's slots instead of holding them for as long as the client pings.
func TestMQTT5SendEndsAStalledClient(t *testing.T) {
	defer func(wait time.Duration) { mqtt5AckWait = wait }(mqtt5AckWait)
	mqtt5AckWait = 200 * time.Millisecond
	server, client := net.Pipe()
	defer client.Close()
	c := &mqtt5Conn{conn: server, state: &mqtt5State{subs: map[string]*mqtt5Sub{}}, slots: make(chan struct{}, 1), acked: make(chan struct{}), maxPacket: 1 << 20, problems: true}
	packets := make(chan mqttwire.Packet, 4)
	go func() {
		reader := bufio.NewReader(client)
		for {
			p, err := mqttwire.ReadV5(reader)
			if err != nil {
				close(packets)
				return
			}
			packets <- p
		}
	}()
	// The first message takes the only slot; the second waits for an
	// acknowledgement that never comes.
	if err := c.send(context.Background(), "airc/room/a", 1, mqttwire.Properties{}, []byte(`{}`), true); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := c.send(context.Background(), "airc/room/a", 1, mqttwire.Properties{}, []byte(`{}`), true); err == nil {
		t.Fatal("a send waited out the stall and succeeded")
	}
	if waited := time.Since(start); waited < mqtt5AckWait || waited > 5*time.Second {
		t.Fatalf("waited %v", waited)
	}
	if p := <-packets; p.Kind != mqttwire.Publish {
		t.Fatalf("first packet %+v", p)
	}
	if p := <-packets; p.Kind != mqttwire.Disconnect || p.Reason != mqttUnspecified {
		t.Fatalf("stalled client got %+v, want DISCONNECT 0x80", p)
	}
	if _, open := <-packets; open {
		t.Fatal("the connection stayed open")
	}
	if len(c.slots) != 1 || len(c.state.inflight) != 1 {
		t.Fatalf("slots %d inflight %d: the unacknowledged message must stay for a resume", len(c.slots), len(c.state.inflight))
	}
}

// A reply to a request made as the session's author is kept until
// acknowledged (QoS 1); one made as another author goes at QoS 0, so a
// resumed session never replays it.
func TestMQTT5ReplyAsAnotherAuthorIsNotKept(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	defer server.Close()
	c := &mqtt5Conn{ctx: t.Context(), conn: server, state: &mqtt5State{owner: "g:bob", subs: map[string]*mqtt5Sub{"replies/bob": {filter: "replies/bob", kind: mqtt5Reply, target: "replies/bob", qos: 1}}}, slots: make(chan struct{}, 4), acked: make(chan struct{}), maxPacket: 1 << 20}
	packets := make(chan mqttwire.Packet, 4)
	go func() {
		reader := bufio.NewReader(client)
		for {
			p, err := mqttwire.ReadV5(reader)
			if err != nil {
				close(packets)
				return
			}
			packets <- p
		}
	}()
	c.reply("replies/bob", []byte("own"), dispatch.DetachedResult{}, nil, true)
	if p := <-packets; p.QoS != 1 || len(c.state.inflight) != 1 {
		t.Fatalf("own reply QoS %d, %d kept", p.QoS, len(c.state.inflight))
	}
	c.reply("replies/bob", []byte("other"), dispatch.DetachedResult{}, nil, false)
	if p := <-packets; p.QoS != 0 || len(c.state.inflight) != 1 {
		t.Fatalf("another author's reply QoS %d, %d kept", p.QoS, len(c.state.inflight))
	}
}
