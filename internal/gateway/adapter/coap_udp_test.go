package adapter

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

func TestCoAPUDPPacketsRejectOuterBoundariesAndPreserveFragment(t *testing.T) {
	receiver, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.Dial("udp", receiver.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	codec := &CoAPUDPPackets{PacketConn: receiver}
	fragments, err := fragmentAGD1([]byte("encrypted frame placeholder"))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*pool.Message){
		"valid":       func(*pool.Message) {},
		"confirmable": func(m *pool.Message) { m.SetType(message.Confirmable) },
		"method":      func(m *pool.Message) { m.SetCode(codes.GET) },
		"path":        func(m *pool.Message) { m.MustSetPath("/wrong") },
		"format":      func(m *pool.Message) { m.SetContentFormat(message.AppJSON) },
		"observe":     func(m *pool.Message) { m.SetObserve(0) },
		"blockwise":   func(m *pool.Message) { m.SetOptionUint32(message.Block1, 0) },
		"fragment":    func(m *pool.Message) { m.SetBody(bytes.NewReader([]byte("bad"))) },
		"oversize":    func(m *pool.Message) { m.SetBody(bytes.NewReader(make([]byte, 1201))) },
	} {
		t.Run(name, func(t *testing.T) {
			req := pool.NewMessage(context.Background())
			req.SetType(message.NonConfirmable)
			req.SetCode(codes.POST)
			req.SetMessageID(1)
			req.MustSetPath("/v1/coap/fragments")
			req.SetContentFormat(message.AppOctets)
			req.SetBody(bytes.NewReader(fragments[0]))
			mutate(req)
			wire, err := req.MarshalWithEncoder(coder.DefaultCoder)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sender.Write(wire); err != nil {
				t.Fatal(err)
			}
			_ = codec.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			dst := make([]byte, 1001)
			n, _, err := codec.ReadFrom(dst)
			if name == "valid" {
				if err != nil || !bytes.Equal(dst[:n], fragments[0]) {
					t.Fatal("fragment changed", err)
				}
			} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				t.Fatal("invalid packet admitted", n, err)
			}
		})
	}
}

func TestCoAPUDPMailboxMIDExhaustionAndIdleReuse(t *testing.T) {
	sender, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	receiver, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	codec := &CoAPUDPPackets{PacketConn: sender, mids: 1 << 16, window: time.Now()}
	if _, err := codec.WriteTo([]byte("fragment"), receiver.LocalAddr()); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("reused live MID", err)
	}
	codec.window = time.Now().Add(-248 * time.Second)
	if _, err := codec.WriteTo([]byte("fragment"), receiver.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_ = receiver.SetReadDeadline(time.Now().Add(time.Second))
	wire := make([]byte, 1201)
	n, _, err := receiver.ReadFrom(wire)
	if err != nil {
		t.Fatal(err)
	}
	req := pool.NewMessage(context.Background())
	if _, err := req.UnmarshalWithDecoder(coder.DefaultCoder, wire[:n]); err != nil {
		t.Fatal(err)
	}
	body, err := req.ReadBody()
	if err != nil || req.MessageID() != 0 || req.Type() != message.NonConfirmable || req.Code() != codes.POST || !bytes.Equal(body, []byte("fragment")) || n != len(body)+coapUDPMailboxOverhead {
		t.Fatal("mailbox encoding", n, req, err)
	}
}
