//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// amqpPeer is an independent AMQP 1.0 client (amqp_wire_fixture_test.go
// holds its codec).
type amqpPeer struct {
	conn                                                net.Conn
	nextIncoming, nextOutgoing, delivery, responseCount uint32
}

// amqpOpen negotiates SASL PLAIN with the listener password when one is
// given (no SASL layer otherwise), opens a session and attaches the response
// and request links. It reports false when the password is refused.
func amqpOpen(t *testing.T, conn net.Conn, password string) (*amqpPeer, bool) {
	t.Helper()
	peer := &amqpPeer{conn: conn, nextIncoming: 1, nextOutgoing: 7, delivery: 70}
	protocols := []byte{0}
	if password != "" {
		protocols = []byte{3, 0}
	}
	for _, protocol := range protocols {
		header := []byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
		aqWrite(t, conn, header)
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		echo := make([]byte, 8)
		if _, err := io.ReadFull(conn, echo); err != nil || string(echo) != string(header) {
			if password == "" {
				return nil, false // the listener requires SASL
			}
			t.Fatal(echo, err)
		}
		if protocol == 3 {
			if p := aqRead(t, conn); p.kind != 0x40 {
				t.Fatal(p)
			}
			aqWrite(t, conn, aqFrame(0x41, 1, [][]byte{aqBytes(0xb3, []byte("PLAIN")), aqBytes(0xb0, []byte("\x00"+fixtureAuthor+"\x00"+password))}, nil))
			if p := aqRead(t, conn); p.kind != 0x44 || aqField(p, 0).number != 0 {
				if p.kind == 0x44 {
					return nil, false
				}
				t.Fatal(p)
			}
		}
	}
	aqWrite(t, conn, aqFrame(0x10, 0, [][]byte{aqBytes(0xb1, []byte("independent-client")), {0x40}, aqUint(512)}, nil))
	if p := aqRead(t, conn); p.kind != 0x10 {
		t.Fatal(p)
	}
	aqWrite(t, conn, aqFrame(0x11, 0, [][]byte{{0x40}, aqUint(7), aqUint(8), aqUint(8)}, nil))
	if p := aqRead(t, conn); p.kind != 0x11 {
		t.Fatal(p)
	}
	// The response link first: the request link's credit then follows both.
	for _, incoming := range []bool{false, true} {
		role, snd, handle, name, source, target := byte(0x42), byte(0), uint32(1), "request-link", "", "request"
		if !incoming {
			role, snd, handle, name, source, target = 0x41, 2, 0, "response-link", "response", ""
		}
		fields := [][]byte{aqBytes(0xb1, []byte(name)), aqUint(handle), {role}, {0x50, snd}, {0x50, 0}, aqDescribed(0x28, aqList(aqBytes(0xb1, []byte(source)))), aqDescribed(0x29, aqList(aqBytes(0xb1, []byte(target)))), {0x40}, {0x42}, aqUint(0)}
		aqWrite(t, conn, aqFrame(0x12, 0, fields, nil))
		if p := aqRead(t, conn); p.kind != 0x12 {
			t.Fatal(p)
		}
	}
	if p := aqRead(t, conn); p.kind != 0x13 || aqField(p, 6).number != 1 {
		t.Fatal(p)
	}
	return peer, true
}
func (p *amqpPeer) flow(t *testing.T) {
	aqWrite(t, p.conn, aqFrame(0x13, 0, [][]byte{aqUint(p.nextIncoming), aqUint(8), aqUint(p.nextOutgoing), aqUint(8), aqUint(0), aqUint(p.responseCount), aqUint(1)}, nil))
}

func (p *amqpPeer) submit(t *testing.T, body []byte) uint32 {
	t.Helper()
	return p.submitMessage(t, aqDescribed(0x75, aqBytes(0xb0, body)))
}

func (p *amqpPeer) submitMessage(t *testing.T, encoded []byte) uint32 {
	t.Helper()
	id := p.delivery
	p.delivery++
	first := true
	frames := 0
	for len(encoded) > 0 {
		n := min(300, len(encoded))
		more := byte(0x42)
		if n < len(encoded) {
			more = 0x41
		}
		fields := [][]byte{aqUint(1), {0x40}, {0x40}, {0x40}, {0x42}, {more}}
		if first {
			fields[1] = aqUint(id)
			fields[2] = aqBytes(0xb0, []byte{byte(id)})
			fields[3] = aqUint(0)
		}
		aqWrite(t, p.conn, aqFrame(0x14, 0, fields, encoded[:n]))
		p.nextOutgoing++
		frames++
		encoded = encoded[n:]
		first = false
		if frames%8 == 0 && len(encoded) > 0 {
			if flow := aqRead(t, p.conn); flow.kind != 0x13 || aqField(flow, 6).number != 0 {
				t.Fatal(flow)
			}
		}
	}
	return id
}

// reply reads the result of delivery after its disposition, settles it and
// returns the disposition's outcome descriptor with the result.
func (p *amqpPeer) reply(t *testing.T, disposition aqPerformative, delivery uint32) (uint64, adapter.AMQPResult) {
	t.Helper()
	if disposition.kind != 0x15 || aqField(disposition, 0).number != 1 || aqField(disposition, 1).number != uint64(delivery) || aqField(disposition, 3).number != 1 {
		t.Fatal(disposition)
	}
	if flow := aqRead(t, p.conn); flow.kind != 0x13 || aqField(flow, 6).number != 0 {
		t.Fatal(flow)
	}
	var encoded []byte
	var responseID uint32
	frames := 0
	for {
		transfer := aqRead(t, p.conn)
		if transfer.kind != 0x14 || aqField(transfer, 0).number != 0 || aqField(transfer, 4).number != 0 {
			t.Fatal(transfer)
		}
		if frames == 0 {
			responseID = uint32(aqField(transfer, 1).number)
		}
		encoded = append(encoded, transfer.payload...)
		frames++
		p.nextIncoming++
		if len(encoded) > 65536 {
			t.Fatal("unbounded AMQP result")
		}
		if aqField(transfer, 5).number == 0 {
			break
		}
		if frames%8 == 0 {
			p.flow(t)
		}
	}
	message := aqDecode(t, bytes.NewReader(encoded), 0)
	if message.descriptor != 0x75 || len(message.data) == 0 {
		t.Fatal("AMQP result data section", message)
	}
	var result adapter.AMQPResult
	if err := json.Unmarshal(message.data, &result); err != nil {
		t.Fatalf("AMQP result is not plain JSON: %v %q", err, message.data)
	}
	if result.Version != 1 || result.Protocol != "amqp/1" || result.RequestAddress != "request" || result.ResponseAddress != "response" || (result.Result == nil) == (result.Error == nil) {
		t.Fatal(result)
	}
	p.responseCount++
	aqWrite(t, p.conn, aqFrame(0x15, 0, [][]byte{{0x41}, aqUint(responseID), {0x40}, {0x41}, aqDescribed(0x24, aqList())}, nil))
	if flow := aqRead(t, p.conn); flow.kind != 0x13 || aqField(flow, 6).number != 1 {
		t.Fatal(flow)
	}
	return aqField(disposition, 4).descriptor, result
}

// amqpSuiteClient sends each request on the request link and reads its
// disposition and result.
type amqpSuiteClient struct{ *amqpPeer }

func dialAMQPSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	t.Helper()
	conn, err := suiteConnect("tcp", d)
	if err != nil {
		return nil, err
	}
	peer, ok := amqpOpen(t, conn, d.password)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("SASL refused")
	}
	return amqpSuiteClient{peer}, nil
}

func (c amqpSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	c.flow(t)
	delivery := c.submit(t, request)
	first := aqRead(t, c.conn)
	if first.kind == 0x16 { // detach: the message itself was refused
		return suiteReply{closed: true}
	}
	_, result := c.reply(t, first, delivery)
	if result.Error != nil {
		return suiteReply{failure: result.Error}
	}
	return suiteReply{result: result.Result}
}

func (c amqpSuiteClient) send(t *testing.T, request []byte) {
	t.Helper()
	c.flow(t)
	c.submit(t, request)
}

func (c amqpSuiteClient) close() { _ = c.conn.Close() }

// amqpSuiteWire covers AMQP's own frames: a send-only link, SASL refusals,
// and the detach condition (or rejected outcome) for each refused message.
func amqpSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("send_only_link", func(t *testing.T) { amqpSendOnlyLink(t, e) })
	t.Run("sasl_refusals", func(t *testing.T) {
		for _, c := range []struct{ mechanism, password string }{{"PLAIN", strings.Repeat("0", 64)}, {"ANONYMOUS", ""}, {"SCRAM-SHA-256", suitePassword}} {
			conn, err := net.DialTimeout("tcp", e.guarded, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			header := []byte{'A', 'M', 'Q', 'P', 3, 1, 0, 0}
			aqWrite(t, conn, header)
			_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
			reply := make([]byte, 8)
			if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != string(header) {
				t.Fatal(reply, err)
			}
			if p := aqRead(t, conn); p.kind != 0x40 {
				t.Fatal(p)
			}
			aqWrite(t, conn, aqFrame(0x41, 1, [][]byte{aqBytes(0xb3, []byte(c.mechanism)), aqBytes(0xb0, []byte("\x00anyone\x00"+c.password))}, nil))
			if p := aqRead(t, conn); p.kind != 0x44 || aqField(p, 0).number != 1 {
				t.Fatal("SASL access refusal", c.mechanism, p)
			}
		}
		// The right password opens a connection that closes cleanly.
		healthy, err := dialAMQPSuite(t, suiteDial{address: e.guarded, password: suitePassword})
		if err != nil {
			t.Fatal(err)
		}
		defer healthy.close()
		conn := healthy.(amqpSuiteClient).conn
		aqWrite(t, conn, aqFrame(0x18, 0, nil, nil))
		if p := aqRead(t, conn); p.kind != 0x18 {
			t.Fatal(p)
		}
	})
	for _, kind := range []string{"malformed", "target", "operation", "reply", "content_type", "content_encoding", "mandatory_annotation", "size"} {
		t.Run("refused_"+kind, func(t *testing.T) {
			client, err := dialAMQPSuite(t, suiteDial{address: e.address})
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()
			peer := client.(amqpSuiteClient).amqpPeer
			id := e.id(kind)
			request := sendRequest(id, "#wire", "must not post", e.guest("wire"), "")
			switch kind {
			case "target":
				request["target"] = map[string]any{"kind": "room", "name": "#other"}
			case "operation":
				request = plainRequest(map[string]any{"request_id": id, "operation": "receipt.get", "lookup_id": "other"}, e.guest("wire"), "")
			}
			body, condition := mustBody(t, request), "amqp:unauthorized-access"
			switch kind {
			case "malformed":
				body, condition = body[:len(body)-1], "amqp:decode-error"
			case "size":
				body, condition = []byte(strings.Repeat("a", 32769)), "amqp:invalid-field"
			}
			message := aqDescribed(0x75, aqBytes(0xb0, body))
			switch kind {
			case "reply":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, aqBytes(0xb1, []byte("other")))), message...)
			case "content_type":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, aqBytes(0xb3, []byte("application/jose")))), message...)
				condition = "amqp:not-implemented"
			case "content_encoding":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, aqBytes(0xb3, []byte("gzip")))), message...)
				condition = "amqp:not-implemented"
			case "mandatory_annotation":
				values := append(aqBytes(0xb3, []byte("required")), []byte{0x41}...)
				// map8: one symbol/boolean pair, count includes both values.
				annotation := append([]byte{0xc1, byte(len(values) + 1), 2}, values...)
				message = append(aqDescribed(0x72, annotation), message...)
				condition = "amqp:not-implemented"
			}
			peer.flow(t)
			delivery := peer.submitMessage(t, message)
			if kind == "target" || kind == "operation" {
				// A request outside the route is rejected with a correlated
				// forbidden result; the connection stays usable.
				outcome, result := peer.reply(t, aqRead(t, peer.conn), delivery)
				if outcome != 0x25 || result.RequestID != id || result.Error == nil || result.Error.Code != core.Forbidden {
					t.Fatal(outcome, result)
				}
				aqWrite(t, peer.conn, aqFrame(0x18, 0, nil, nil))
			} else {
				detach := aqRead(t, peer.conn)
				if detach.kind != 0x16 || aqField(detach, 1).number != 1 || len(aqField(detach, 2).fields) != 1 || string(aqField(detach, 2).fields[0].data) != condition {
					t.Fatal(detach)
				}
				if close := aqRead(t, peer.conn); close.kind != 0x18 {
					t.Fatal(close)
				}
				aqWrite(t, peer.conn, aqFrame(0x18, 0, nil, nil))
			}
			e.noJob(t, id)
		})
	}
}

// amqpSendOnlyLink: without a SASL layer, with a Flow pipelined before the
// server's Begin, a send-only link gets credit and each disposition reports
// acceptance.
func amqpSendOnlyLink(t *testing.T, e *suiteEnv) {
	am, err := net.DialTimeout("tcp", e.address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer am.Close()
	header := []byte{'A', 'M', 'Q', 'P', 0, 1, 0, 0}
	aqWrite(t, am, header)
	echo := make([]byte, 8)
	if _, err := io.ReadFull(am, echo); err != nil || string(echo) != string(header) {
		t.Fatal(echo, err)
	}
	aqWrite(t, am, aqFrame(0x10, 0, [][]byte{aqBytes(0xb1, []byte("suite-wire")), {0x40}, aqUint(4096)}, nil))
	aqWrite(t, am, aqFrame(0x11, 0, [][]byte{{0x40}, aqUint(1), aqUint(8), aqUint(8)}, nil))
	// Pipelined before the server's Begin: next-incoming-id is absent.
	aqWrite(t, am, aqFrame(0x13, 0, [][]byte{{0x40}, aqUint(8), aqUint(1), aqUint(8)}, nil))
	if p := aqRead(t, am); p.kind != 0x10 {
		t.Fatal(p)
	}
	if p := aqRead(t, am); p.kind != 0x11 {
		t.Fatal(p)
	}
	attach := [][]byte{aqBytes(0xb1, []byte("sender")), aqUint(0), {0x42}, {0x50, 2}, {0x50, 0}, aqDescribed(0x28, aqList(aqBytes(0xb1, []byte("")))), aqDescribed(0x29, aqList(aqBytes(0xb1, []byte("request")))), {0x40}, {0x42}, aqUint(0)}
	aqWrite(t, am, aqFrame(0x12, 0, attach, nil))
	if p := aqRead(t, am); p.kind != 0x12 {
		t.Fatal(p)
	}
	if p := aqRead(t, am); p.kind != 0x13 || aqField(p, 6).number != 1 {
		t.Fatal("send-only link got no credit", p)
	}
	for i, c := range []struct{ nick, token string }{{e.guest("wire"), ""}, {"alice", aliceToken}} {
		id, text := e.id("send-only"), e.text(fmt.Sprint("send-only ", i))
		// An amqp-value string body, as clients send by default.
		message := aqDescribed(0x77, aqBytes(0xb1, sendBody(t, id, "#wire", text, c.nick, c.token)))
		aqWrite(t, am, aqFrame(0x14, 0, [][]byte{aqUint(0), aqUint(uint32(i)), aqBytes(0xb0, []byte{byte(i)}), aqUint(0), {0x42}, {0x42}}, message))
		if disposition := aqRead(t, am); disposition.kind != 0x15 || aqField(disposition, 4).descriptor != 0x24 {
			t.Fatal(disposition)
		}
		for {
			flow := aqRead(t, am)
			if flow.kind != 0x13 {
				t.Fatal(flow)
			}
			if aqField(flow, 6).number == 1 {
				break
			}
		}
		e.posted(t, id, "#wire", text, c.nick, c.token)
	}
}
