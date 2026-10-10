//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc/credentials"
)

type grpcPressureEvent struct {
	at      time.Time
	pending uint32
	err     error
}

// Observe decrypted HTTP/2 frames underneath the real gRPC client. Only this
// connection's stream 1 is used; its advertised credit and replenishments are
// compared with actual DATA bytes, including flow-controlled padding.
type grpcPressureCredentials struct {
	credentials.TransportCredentials
	events chan grpcPressureEvent
}

func (c *grpcPressureCredentials) Clone() credentials.TransportCredentials {
	return &grpcPressureCredentials{TransportCredentials: c.TransportCredentials.Clone(), events: c.events}
}

func (c *grpcPressureCredentials) ClientHandshake(ctx context.Context, authority string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	secure, info, err := c.TransportCredentials.ClientHandshake(ctx, authority, conn)
	if err != nil {
		return nil, nil, err
	}
	return &grpcPressureConn{Conn: secure, events: c.events, credit: 65535, preface: 24}, info, nil
}

type grpcPressureConn struct {
	net.Conn
	mu          sync.Mutex
	events      chan grpcPressureEvent
	in, out     []byte
	preface     int
	credit      int64
	header      [5]byte
	headerBytes int
	pending     uint32
	reported    bool
}

func (c *grpcPressureConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	c.observe(data[:n], false)
	return n, err
}

func (c *grpcPressureConn) Write(data []byte) (int, error) {
	// Account replenishment before forwarding it: the peer can use that credit
	// before Write returns. Overcount after a failed write is conservative.
	c.observe(data, true)
	return c.Conn.Write(data)
}

func (c *grpcPressureConn) report(err error) {
	if c.reported {
		return
	}
	c.reported = true
	select {
	case c.events <- grpcPressureEvent{at: time.Now(), pending: c.pending, err: err}:
	default:
	}
}

func (c *grpcPressureConn) observe(data []byte, outgoing bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reported {
		return
	}
	buffer := &c.in
	if outgoing {
		skip := min(c.preface, len(data))
		c.preface -= skip
		data = data[skip:]
		buffer = &c.out
	}
	*buffer = append(*buffer, data...)
	for len(*buffer) >= 9 {
		frame := *buffer
		length := int(frame[0])<<16 | int(frame[1])<<8 | int(frame[2])
		if length > 1<<20 {
			c.report(fmt.Errorf("unexpected HTTP/2 frame size %d", length))
			return
		}
		if len(frame) < 9+length {
			return
		}
		kind, flags := frame[3], frame[4]
		stream := binary.BigEndian.Uint32(frame[5:9]) & 0x7fffffff
		payload := frame[9 : 9+length]
		if outgoing && kind == 4 && flags&1 == 0 {
			for offset := 0; offset+6 <= len(payload); offset += 6 {
				if binary.BigEndian.Uint16(payload[offset:]) == 4 {
					// This client explicitly advertises the default static stream window.
					if window := binary.BigEndian.Uint32(payload[offset+2:]); window != 65535 {
						c.report(fmt.Errorf("unexpected initial stream window %d", window))
						return
					}
				}
			}
		}
		if outgoing && kind == 8 && stream == 1 && len(payload) == 4 {
			c.credit += int64(binary.BigEndian.Uint32(payload) & 0x7fffffff)
		}
		if !outgoing && kind == 0 && stream == 1 {
			c.credit -= int64(length)
			message := payload
			if flags&8 != 0 {
				if len(message) == 0 || int(message[0])+1 > len(message) {
					c.report(fmt.Errorf("invalid DATA padding"))
					return
				}
				message = message[1 : len(message)-int(message[0])]
			}
			for len(message) > 0 {
				if c.pending > 0 {
					take := min(int(c.pending), len(message))
					c.pending -= uint32(take)
					message = message[take:]
				} else {
					take := min(5-c.headerBytes, len(message))
					copy(c.header[c.headerBytes:], message[:take])
					c.headerBytes += take
					message = message[take:]
					if c.headerBytes == 5 {
						c.pending = binary.BigEndian.Uint32(c.header[1:])
						c.headerBytes = 0
					}
				}
			}
			if c.credit < 0 {
				c.report(fmt.Errorf("HTTP/2 stream credit went negative: %d", c.credit))
				return
			}
			if c.credit == 0 {
				c.report(nil)
				return
			}
		}
		*buffer = frame[9+length:]
	}
}
