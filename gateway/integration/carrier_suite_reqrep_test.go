//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/config"
)

// ZeroMQ (ZMTP 3.1 REQ, NULL mechanism) and NNG (SP REQ0 over TCP) are
// serial request/reply sockets: each request gets one whole reply, and a
// connection keeps an identified author until it closes. Like IRC they need
// no TLS.
var _ = registerSuiteCarriers(
	suiteCarrier{name: "zeromq", listener: config.Listener{Protocol: "zeromq"}, dial: dialREQSuite(false), results: true, reads: true, session: true, wire: reqSuiteWire},
	suiteCarrier{name: "nng", listener: config.Listener{Protocol: "nng"}, dial: dialREQSuite(true), results: true, reads: true, session: true, wire: reqSuiteWire},
)

// reqSuiteClient is an independent REQ peer of either wire.
type reqSuiteClient struct {
	conn net.Conn
	nng  bool
	id   uint32 // the last NNG request ID
}

func dialREQSuite(nng bool) func(*testing.T, suiteDial) (suiteClient, error) {
	return func(_ *testing.T, d suiteDial) (suiteClient, error) {
		conn, err := suiteConnect("tcp", d)
		if err != nil {
			return nil, err
		}
		c := &reqSuiteClient{conn: conn, nng: nng, id: 0x80000000}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := c.handshake(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		_ = conn.SetDeadline(time.Time{})
		return c, nil
	}
}

// handshake exchanges the SP header (NNG) or the ZMTP greeting and READY.
func (c *reqSuiteClient) handshake() error {
	if c.nng {
		if _, err := c.conn.Write([]byte{0, 'S', 'P', 0, 0, 0x30, 0, 0}); err != nil {
			return err
		}
		header := make([]byte, 8)
		if _, err := io.ReadFull(c.conn, header); err != nil || !bytes.Equal(header, []byte{0, 'S', 'P', 0, 0, 0x31, 0, 0}) {
			return fmt.Errorf("SP header %x: %v", header, err)
		}
		return nil
	}
	greeting := make([]byte, 64)
	greeting[0], greeting[9], greeting[10], greeting[11] = 0xff, 0x7f, 3, 1
	copy(greeting[12:], "NULL")
	ready := append([]byte{5}, "READY"...)
	ready = append(append(ready, 11), "Socket-Type"...)
	ready = append(binary.BigEndian.AppendUint32(ready, 3), "REQ"...)
	if _, err := c.conn.Write(append(greeting, append([]byte{4, byte(len(ready))}, ready...)...)); err != nil {
		return err
	}
	peer := make([]byte, 64)
	if _, err := io.ReadFull(c.conn, peer); err != nil || peer[0] != 0xff || peer[10] != 3 || string(peer[12:16]) != "NULL" {
		return fmt.Errorf("ZMTP greeting %x: %v", peer, err)
	}
	command, err := c.zmtpFrame()
	if err != nil || len(command) < 6 || string(command[1:6]) != "READY" {
		return fmt.Errorf("ZMTP READY %q: %v", command, err)
	}
	return nil
}

// zmtpFrame reads one ZMTP frame body.
func (c *reqSuiteClient) zmtpFrame() ([]byte, error) {
	var header [9]byte
	if _, err := io.ReadFull(c.conn, header[:2]); err != nil {
		return nil, err
	}
	size := uint64(header[1])
	if header[0]&2 != 0 {
		if _, err := io.ReadFull(c.conn, header[2:9]); err != nil {
			return nil, err
		}
		size = binary.BigEndian.Uint64(header[1:9])
	}
	body := make([]byte, size)
	_, err := io.ReadFull(c.conn, body)
	return body, err
}

func (c *reqSuiteClient) send(t *testing.T, request []byte) {
	t.Helper()
	var wire []byte
	if c.nng {
		c.id++
		wire = binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint64(nil, uint64(len(request)+4)), c.id)
	} else if wire = []byte{1, 0}; len(request) < 256 { // empty delimiter, then the body
		wire = append(wire, 0, byte(len(request)))
	} else {
		wire = binary.BigEndian.AppendUint64(append(wire, 2), uint64(len(request)))
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write(append(wire, request...)); err != nil {
		t.Fatal(err)
	}
}

func (c *reqSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	c.send(t, request)
	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	body, err := c.reply()
	if err != nil {
		if peerClosed(err) {
			return suiteReply{closed: true}
		}
		t.Fatal(err)
	}
	var reply adapter.LocalREPResponse
	if err := json.Unmarshal(body, &reply); err != nil || reply.Version != 1 || reply.Result == nil && reply.Error == nil {
		t.Fatalf("REP reply %s: %v", body, err)
	}
	if reply.Error != nil {
		return suiteReply{failure: reply.Error}
	}
	return suiteReply{result: reply.Result}
}

// reply reads one whole reply: an NNG message echoing the request ID, or a
// ZMTP empty delimiter and body.
func (c *reqSuiteClient) reply() ([]byte, error) {
	if c.nng {
		header := make([]byte, 12)
		if _, err := io.ReadFull(c.conn, header); err != nil {
			return nil, err
		}
		if id := binary.BigEndian.Uint32(header[8:]); id != c.id {
			return nil, fmt.Errorf("NNG reply to request %x, want %x", id, c.id)
		}
		body := make([]byte, binary.BigEndian.Uint64(header[:8])-4)
		_, err := io.ReadFull(c.conn, body)
		return body, err
	}
	if delimiter, err := c.zmtpFrame(); err != nil || len(delimiter) != 0 {
		return nil, fmt.Errorf("REP delimiter %q: %v", delimiter, err)
	}
	return c.zmtpFrame()
}

func (c *reqSuiteClient) close() { _ = c.conn.Close() }

// reqSuiteWire: a ZMTP heartbeat is answered with its context, and a
// connection idle beyond one message's deadline stays usable.
func reqSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("idle_connection", func(t *testing.T) {
		client := e.dial(t).(*reqSuiteClient)
		if !client.nng {
			ping := append([]byte("\x04PING\x00\x00"), "hb"...)
			if _, err := client.conn.Write(append([]byte{4, byte(len(ping))}, ping...)); err != nil {
				t.Fatal(err)
			}
			_ = client.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if pong, err := client.zmtpFrame(); err != nil || string(pong) != "\x04PONGhb" {
				t.Fatalf("PONG %q: %v", pong, err)
			}
		}
		time.Sleep(3500 * time.Millisecond)
		id, text := e.id("idle"), e.text("idle")
		e.admitted(t, client.call(t, sendBody(t, id, "#wire", text, e.guest("wire"), "")), id)
		e.posted(t, id, "#wire", text, e.guest("wire"), "")
	})
}
