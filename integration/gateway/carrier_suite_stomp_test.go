//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

// stompPeer is an independent STOMP 1.2 client (stomp_wire_fixture_test.go
// holds its codec).
type stompPeer struct {
	conn   net.Conn
	reader *bufio.Reader
}

// stompOpen sends CONNECT, with the listener password as the passcode when
// one is given, and subscribes to the route's response destination. A
// refused CONNECT returns the ERROR frame instead.
func stompOpen(t *testing.T, conn net.Conn, passcode string) (*stompPeer, *sqFrame) {
	t.Helper()
	p := &stompPeer{conn: conn, reader: bufio.NewReader(conn)}
	// Login and host carry no authority; any host is accepted.
	headers := []string{"accept-version:1.2", "host:any-host.example", "login:anyone", "heart-beat:0,0"}
	if passcode != "" {
		headers = append(headers, "passcode:"+passcode)
	}
	sqWrite(t, conn, "CONNECT", headers, nil)
	if frame := p.read(t); frame.command == "ERROR" {
		return nil, &frame
	} else if frame.command != "CONNECTED" || frame.headers["version"] != "1.2" || frame.headers["heart-beat"] != "1000,1000" {
		t.Fatal(frame)
	}
	sqWrite(t, conn, "SUBSCRIBE", []string{"id:result", "destination:response", "ack:client-individual", "receipt:subscribed"}, nil)
	if frame := p.read(t); frame.command != "RECEIPT" || frame.headers["receipt-id"] != "subscribed" {
		t.Fatal(frame)
	}
	return p, nil
}

func (p *stompPeer) read(t *testing.T) sqFrame {
	t.Helper()
	if err := p.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	f, err := sqDecode(p.reader)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// stompSuiteClient sends each request to the route's request destination
// with a receipt and reads the result from its subscription.
type stompSuiteClient struct {
	*stompPeer
	sequence int
}

func dialSTOMPSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	t.Helper()
	conn, err := suiteConnect("tcp", d)
	if err != nil {
		return nil, err
	}
	peer, refusal := stompOpen(t, conn, d.password)
	if refusal != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("CONNECT refused: %v", refusal.headers["message"])
	}
	return &stompSuiteClient{stompPeer: peer}, nil
}

func (c *stompSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	receipt := c.write(t, request)
	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	frame, err := sqDecode(c.reader)
	if err != nil {
		if peerClosed(err) {
			return suiteReply{closed: true}
		}
		t.Fatal(err)
	}
	switch {
	case frame.command == "RECEIPT" && frame.headers["receipt-id"] == receipt:
		// The receipt follows committed intake; the result follows it.
		if frame = c.read(t); frame.command != "MESSAGE" || frame.headers["destination"] != "response" || frame.headers["subscription"] != "result" || frame.headers["ack"] == "" || frame.headers["message-id"] == "" {
			t.Fatal(frame)
		}
		sqWrite(t, c.conn, "ACK", []string{"id:" + frame.headers["ack"], "receipt:acked"}, nil)
		if ack := c.read(t); ack.command != "RECEIPT" || ack.headers["receipt-id"] != "acked" {
			t.Fatal(ack)
		}
	case frame.command == "ERROR":
		// A refused request carries its result; a refused frame carries
		// only a message. Either way the connection ends.
		if frame.headers["receipt-id"] != receipt || frame.headers["content-type"] != "application/json" {
			return suiteReply{closed: true}
		}
	default:
		t.Fatal(frame)
	}
	var result adapter.STOMPResult
	if err := json.Unmarshal(frame.body, &result); err != nil || result.Version != 1 || result.Protocol != "stomp/1.2" || result.RequestDestination != "request" || result.ResponseDestination != "response" || (result.Result == nil) == (result.Error == nil) || (frame.command == "ERROR") != (result.Error != nil) {
		t.Fatalf("STOMP result %s: %v", frame.body, err)
	}
	if result.Error != nil {
		return suiteReply{failure: result.Error}
	}
	return suiteReply{result: result.Result}
}

func (c *stompSuiteClient) write(t *testing.T, request []byte) string {
	t.Helper()
	c.sequence++
	receipt := fmt.Sprint("r", c.sequence)
	sqWrite(t, c.conn, "SEND", []string{"destination:request", "content-type:application/json", "receipt:" + receipt}, request)
	return receipt
}

func (c *stompSuiteClient) send(t *testing.T, request []byte) { c.write(t, request) }

func (c *stompSuiteClient) close() { _ = c.conn.Close() }

// stompSuiteWire covers STOMP's own frames: receipts without a
// subscription, CONNECT refusals, negotiated heartbeats and DISCONNECT, and
// the ERROR frame (then close) for each refused SEND.
func stompSuiteWire(t *testing.T, e *suiteEnv) {
	connect := func(t *testing.T, address string) (net.Conn, *bufio.Reader) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", address, 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		return conn, bufio.NewReader(conn)
	}
	t.Run("send_without_subscription", func(t *testing.T) {
		conn, reader := connect(t, e.address)
		sqWrite(t, conn, "CONNECT", []string{"accept-version:1.2", "host:any-host.example", "heart-beat:0,0"}, nil)
		if frame, err := sqDecode(reader); err != nil || frame.command != "CONNECTED" {
			t.Fatal(frame, err)
		}
		id, text := e.id("unsubscribed"), e.text("unsubscribed")
		sqWrite(t, conn, "SEND", []string{"destination:request", "content-type:application/json", "receipt:guest"}, sendBody(t, id, "#wire", text, e.guest("wire"), ""))
		if frame, err := sqDecode(reader); err != nil || frame.command != "RECEIPT" || frame.headers["receipt-id"] != "guest" {
			t.Fatal(frame, err)
		}
		e.posted(t, id, "#wire", text, e.guest("wire"), "")
	})
	t.Run("connect_refusals", func(t *testing.T) {
		for _, headers := range [][]string{
			{"accept-version:1.2", "passcode:" + strings.Repeat("0", 64)},
			{"accept-version:1.2", "login:anyone"},
			{"accept-version:1.1", "passcode:" + suitePassword},
		} {
			conn, reader := connect(t, e.guarded)
			sqWrite(t, conn, "CONNECT", append(headers, "host:any-host.example", "heart-beat:0,0"), nil)
			if frame, err := sqDecode(reader); err != nil || frame.command != "ERROR" || frame.headers["message"] == "" {
				t.Fatal(headers, frame, err)
			}
			sqClosed(t, conn, reader)
		}
	})
	t.Run("heartbeat_and_disconnect", func(t *testing.T) {
		// Negotiate only server heartbeats and observe an actual EOL.
		conn, reader := connect(t, e.address)
		sqWrite(t, conn, "CONNECT", []string{"accept-version:1.2", "host:any-host.example", "heart-beat:0,1000"}, nil)
		if frame, err := sqDecode(reader); err != nil || frame.command != "CONNECTED" || frame.headers["heart-beat"] != "1000,1000" {
			t.Fatal(frame, err)
		}
		if heartbeat, err := reader.ReadByte(); err != nil || heartbeat != '\n' {
			t.Fatal("missing negotiated heartbeat", heartbeat, err)
		}
		sqWrite(t, conn, "DISCONNECT", []string{"receipt:bye"}, nil)
		if frame, err := sqDecode(reader); err != nil || frame.command != "RECEIPT" || frame.headers["receipt-id"] != "bye" {
			t.Fatal(frame, err)
		}
		sqClosed(t, conn, reader)
	})
	for _, kind := range []string{"malformed", "target", "operation", "reply", "content_type", "transaction", "size"} {
		t.Run("refused_"+kind, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", e.address, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			peer, _ := stompOpen(t, conn, "")
			id := e.id(kind)
			request := sendRequest(id, "#wire", "must not post", e.guest("wire"), "")
			switch kind {
			case "target":
				request["target"] = map[string]any{"kind": "room", "name": "#other"}
			case "operation":
				request = plainRequest(map[string]any{"request_id": id, "operation": "receipt.get", "lookup_id": "other"}, e.guest("wire"), "")
			}
			body := mustBody(t, request)
			headers := []string{"destination:request", "content-type:application/json", "receipt:refused"}
			message := ""
			switch kind {
			case "malformed":
				body, message = body[:len(body)-1], "invalid STOMP request body"
			case "target", "operation":
				// A request outside the route fails with its result.
				message = "STOMP request failed"
			case "reply":
				headers, message = append(headers, "reply-to:other"), "STOMP reply-to must be the route's response destination"
			case "content_type":
				headers[1], message = "content-type:application/jose", "unsupported STOMP content type"
			case "transaction":
				headers, message = append(headers, "transaction:unsupported"), "STOMP transactions are not supported"
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if kind == "size" {
				// A framing refusal may close while this oversized body is written.
				_, _ = conn.Write([]byte("SEND\ndestination:request\ncontent-length:32769\n\n" + strings.Repeat("a", 32769) + "\x00"))
			} else {
				sqWrite(t, conn, "SEND", headers, body)
				if frame := peer.read(t); frame.command != "ERROR" || frame.headers["message"] != message {
					t.Fatal(frame)
				}
			}
			sqClosed(t, conn, peer.reader)
			e.noJob(t, id)
		})
	}
}
