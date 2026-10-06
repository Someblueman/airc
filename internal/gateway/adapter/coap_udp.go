package adapter

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

const coapUDPMailboxOverhead = 23

// CoAPUDPPackets carries AGD1 fragments in NON POSTs. The packet driver supplies
// detached authentication and fixed, signed/encrypted mailbox return routing.
// No CoAP acknowledgement is emitted or interpreted as durable intake.
type CoAPUDPPackets struct {
	net.PacketConn
	mu     sync.Mutex
	mids   uint32
	window time.Time
}

func (c *CoAPUDPPackets) ReadFrom(dst []byte) (int, net.Addr, error) {
	wire := make([]byte, 1201)
	for {
		n, addr, err := c.PacketConn.ReadFrom(wire)
		if err != nil {
			return 0, nil, err
		}
		if n > 1200 {
			continue
		}
		req := pool.NewMessage(context.Background())
		consumed, err := req.UnmarshalWithDecoder(coder.DefaultCoder, wire[:n])
		if err != nil || consumed != n || req.Type() != message.NonConfirmable || req.Code() != codes.POST {
			continue
		}
		path, err := req.Path()
		if err != nil || path != "/v1/coap/fragments" {
			continue
		}
		format, err := req.ContentFormat()
		if err != nil || format != message.AppOctets {
			continue
		}
		// This profile does not combine AGD1 with CoAP Blockwise or Observe state.
		valid := true
		for _, option := range req.Options() {
			switch option.ID {
			case message.URIPath, message.ContentFormat:
			default:
				valid = false
			}
		}
		if !valid {
			continue
		}
		payload, err := req.ReadBody()
		if err != nil || len(payload) > 1000 {
			continue
		}
		if _, _, _, _, _, err := parseAGD1Datagram(payload); err != nil {
			continue
		}
		if len(payload) > len(dst) {
			return 0, nil, io.ErrShortBuffer
		}
		return copy(dst, payload), addr, nil
	}
}

func (c *CoAPUDPPackets) WriteTo(payload []byte, addr net.Addr) (int, error) {
	if len(payload) > 1000 {
		return 0, store.ErrCapacity
	}
	// Do not reuse a NON MID within its exchange lifetime, even under sustained
	// load. Exhaustion is an honest response-capacity failure, never a new ID.
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.window.IsZero() || now.Sub(c.window) >= 247*time.Second {
		c.window = now
		c.mids = 0
	}
	if c.mids >= 1<<16 {
		return 0, store.ErrCapacity
	}
	c.window = now
	mid := c.mids
	c.mids++
	req := pool.NewMessage(context.Background())
	req.SetType(message.NonConfirmable)
	req.SetCode(codes.POST)
	req.SetMessageID(int32(mid))
	if err := req.SetPath("/v1/coap/mailbox"); err != nil {
		return 0, err
	}
	req.SetContentFormat(message.AppOctets)
	req.SetBody(bytes.NewReader(payload))
	wire, err := req.MarshalWithEncoder(coder.DefaultCoder)
	if err != nil {
		return 0, err
	}
	if len(wire) != len(payload)+coapUDPMailboxOverhead || len(wire) > 1200 {
		return 0, store.ErrCapacity
	}
	n, err := c.PacketConn.WriteTo(wire, addr)
	if err != nil {
		return 0, err
	}
	if n != len(wire) {
		return 0, io.ErrShortWrite
	}
	return len(payload), nil
}
