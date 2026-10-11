//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

const suiteMQTTTopic = "airc/send"

// mqttSuiteClient publishes each request at QoS 1 on the route topic. MQTT
// returns no results: a PUBACK acknowledges durable intake and a refusal
// closes the connection.
type mqttSuiteClient struct {
	conn   net.Conn
	packet uint16
}

var mqttClientIDs atomic.Int64

func dialMQTTSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	t.Helper()
	conn, err := mqttConnect(t, d, fmt.Sprint("suite-", mqttClientIDs.Add(1)))
	if err != nil {
		return nil, err
	}
	return &mqttSuiteClient{conn: conn}, nil
}

// mqttConnect sends CONNECT (clean session, the password if any) and
// returns the connection once CONNACK accepts it.
func mqttConnect(t *testing.T, d suiteDial, clientID string) (net.Conn, error) {
	t.Helper()
	conn, err := suiteConnect("tcp", d)
	if err != nil {
		return nil, err
	}
	body := mqttString([]byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0x02, 0, 30}, clientID)
	if d.password != "" {
		body[7] = 0xc2
		body = mqttString(mqttString(body, "anyone"), d.password)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(mqttPacket(0x10, body)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	connack := make([]byte, 4)
	if _, err := io.ReadFull(conn, connack); err != nil || !bytes.Equal(connack, []byte{0x20, 2, 0, 0}) {
		_ = conn.Close()
		return nil, fmt.Errorf("CONNACK %x: %v", connack, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (c *mqttSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	c.send(t, request)
	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	reply := make([]byte, 4)
	if _, err := io.ReadFull(c.conn, reply); err != nil {
		if peerClosed(err) {
			return suiteReply{closed: true}
		}
		t.Fatal(err)
	}
	if !bytes.Equal(reply, []byte{0x40, 2, byte(c.packet >> 8), byte(c.packet)}) {
		t.Fatalf("MQTT reply %x", reply)
	}
	return suiteReply{acked: true}
}

func (c *mqttSuiteClient) send(t *testing.T, request []byte) {
	t.Helper()
	c.packet++
	mqttWrite(t, c.conn, mqttPublication(c.packet, suiteMQTTTopic, string(request), false))
}

func (c *mqttSuiteClient) close() { _ = c.conn.Close() }

func peerClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)
}

// mqttSuiteWire covers MQTT's own packets: QoS 0, client ID takeover,
// and closing without a PUBACK for each refused packet.
func mqttSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("qos0", func(t *testing.T) {
		// QoS 0 is admitted at most once without an acknowledgement, and
		// packets are handled in order, so a PINGRESP follows the admission.
		client := e.dial(t).(*mqttSuiteClient)
		id, text := e.id("qos0"), e.text("qos0")
		mqttWrite(t, client.conn, mqttPacket(0x30, append(mqttString(nil, suiteMQTTTopic), sendBody(t, id, "#wire", text, "alice", aliceToken)...)))
		mqttWrite(t, client.conn, []byte{0xc0, 0})
		mqttReply(t, client.conn, []byte{0xd0, 0})
		e.posted(t, id, "#wire", text, "alice", aliceToken)
	})
	t.Run("client_id_takeover", func(t *testing.T) {
		// A connection with an existing client ID replaces the old one
		// before CONNACK; its duplicate publication is acknowledged without
		// a second delivery.
		first, err := mqttConnect(t, suiteDial{address: e.address}, "wire-client")
		if err != nil {
			t.Fatal(err)
		}
		defer first.Close()
		id, text := e.id("duplicate"), e.text("duplicate")
		payload := string(sendBody(t, id, "#wire", text, e.guest("wire"), ""))
		mqttWrite(t, first, mqttPublication(7, suiteMQTTTopic, payload, false))
		mqttReply(t, first, []byte{0x40, 2, 0, 7})
		replacement, err := mqttConnect(t, suiteDial{address: e.address}, "wire-client")
		if err != nil {
			t.Fatal(err)
		}
		defer replacement.Close()
		mqttPeerClosed(t, first)
		mqttWrite(t, replacement, mqttPublication(8, suiteMQTTTopic, payload, true))
		mqttReply(t, replacement, []byte{0x40, 2, 0, 8})
		e.posted(t, id, "#wire", text, e.guest("wire"), "")
		if n := e.nativeCount(t, "#wire", text); n != 1 {
			t.Fatalf("AIRC holds %d messages for one request ID", n)
		}
	})
	for _, kind := range []string{"topic", "target", "anonymous", "malformed", "operation", "zero_id", "qos2", "retain", "second_connect", "oversize"} {
		t.Run("refused_"+kind, func(t *testing.T) {
			client := e.dial(t).(*mqttSuiteClient)
			id := e.id(kind)
			request := sendRequest(id, "#wire", "must not post", e.guest("wire"), "")
			switch kind {
			case "target":
				request["target"] = map[string]any{"kind": "room", "name": "#other"}
			case "anonymous":
				delete(request, "author")
			case "operation":
				request = plainRequest(map[string]any{"request_id": id, "operation": "receipt.get", "lookup_id": "missing"}, e.guest("wire"), "")
			}
			payload := string(mustBody(t, request))
			packet := mqttPublication(1, suiteMQTTTopic, payload, false)
			switch kind {
			case "topic":
				packet = mqttPublication(1, "airc/unknown", payload, false)
			case "malformed":
				packet = mqttPublication(1, suiteMQTTTopic, payload[:len(payload)-1], false)
			case "zero_id":
				packet = mqttPublication(0, suiteMQTTTopic, payload, false)
			case "qos2":
				packet[0] = 0x34
			case "retain":
				packet[0] |= 1
			case "second_connect":
				packet = []byte{0x10, 0}
			case "oversize":
				packet = []byte{0x32, 0x85, 0x81, 0x02} // 32,901 declared bytes; no body.
			}
			mqttWrite(t, client.conn, packet)
			mqttPeerClosed(t, client.conn)
			e.noJob(t, id)
		})
	}
}

// The gateway's accepted-socket capacity counts a connection that has not
// sent CONNECT, refuses the next one, and joins both open sockets at
// shutdown.
func TestMQTTGlobalAcceptedSocketCapacityAndJoinedPreauth(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "mqtt", Protocol: "mqtt", MQTT: &config.MQTT{Routes: []config.MQTTRoute{{Topic: suiteMQTTTopic}}}})
	c.Limits.Workers, c.Limits.Sessions, c.Limits.SessionsPerAuthor = 1, 2, 1
	c.Limits.Watches, c.Limits.WatchesPerAuthor = 0, 0
	g := startAuthorGateway(t, c)
	address := suiteDial{address: g.addresses["mqtt"]}
	owner, err := mqttConnect(t, address, "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	preauth, err := net.DialTimeout("tcp", address.address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer preauth.Close()
	if refused, err := mqttConnect(t, address, "third"); err == nil {
		_ = refused.Close()
		t.Fatal("MQTT exceeded accepted socket capacity")
	}
	mqttWrite(t, owner, []byte{0xc0, 0})
	mqttReply(t, owner, []byte{0xd0, 0})
	g.stop(t)
	mqttPeerClosed(t, owner)
	mqttPeerClosed(t, preauth)
}
