//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// freeUDP reserves a loopback UDP address for a listener.
func freeUDP(t *testing.T) string {
	t.Helper()
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	return probe.LocalAddr().String()
}

func plainFrameBytes(t *testing.T, id string, request map[string]any) []byte {
	t.Helper()
	frame, err := json.Marshal(map[string]any{"version": 1, "correlation_id": id, "profile": "plain", "request": request})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func decodePlainFrame(t *testing.T, id string, data []byte) plainFrameResponse {
	t.Helper()
	var response plainFrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.CorrelationID != id {
		t.Fatalf("frame reply %s: %v", data, err)
	}
	return response
}

func mustBody(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// seenFrom waits for an independent IRC observer to see text, and returns
// who AIRC says posted it.
func seenFrom(t *testing.T, watcher *irc.Client, text string) string {
	t.Helper()
	return next(t, watcher, func(e *irc.MessageEvent) bool { return e.Message == text }).From
}

func agd1(t *testing.T, body []byte) [][]byte {
	t.Helper()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return webTransportDatagrams(body, id)
}

// Every network carrier takes the same plain contract JSON with no
// gateway credential: a nickname alone posts as an
// AIRC guest and a nickname with its account token posts as the account.
// Connection carriers keep an identified author for the connection. An
// independent IRC client observes who AIRC says posted each message.
// Carriers registered in TestCarrierSuite are covered there instead.
func TestAuthorNetworkCarriersPostAsAuthors(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	watcher := dial(t, socket, "watcher", "", false, false)
	if err := watcher.Join("#lobby"); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" })
	provision := dial(t, socket, "alice", aliceToken, true, true)
	_ = provision.Close()

	lobby := core.Target{Kind: "room", Name: "#lobby"}
	c := authorConfig(t, socket, gatewayToken,
		config.Listener{ID: "web", Protocol: "http"},
		config.Listener{ID: "udp", Protocol: "udp", Address: freeUDP(t)},
		config.Listener{ID: "zmq", Protocol: "zeromq"},
		config.Listener{ID: "nng", Protocol: "nng"},
		config.Listener{ID: "sn", Protocol: "mqtt_sn", Address: freeUDP(t), Operations: []core.Operation{core.Send}, MQTTSN: &config.MQTTSN{Routes: []config.MQTTSNRoute{{TopicID: 7, Target: lobby}}}},
	)
	g := startAuthorGateway(t, c)

	t.Run("udp", func(t *testing.T) {
		conn, err := net.Dial("udp", g.addresses["udp"])
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, post := range []struct{ id, nick, token, text string }{
			{"udp-guest", "udp-guest", "", "guest over UDP"},
			{"udp-alice", "alice", aliceToken, "alice over UDP"},
		} {
			for _, fragment := range agd1(t, plainFrameBytes(t, post.id, sendRequest(post.id, "#lobby", post.text, post.nick, post.token))) {
				if _, err := conn.Write(fragment); err != nil {
					t.Fatal(err)
				}
			}
			if r := decodePlainFrame(t, post.id, readAGD1Payload(t, conn, time.Now().Add(10*time.Second))); r.Event != "result" {
				t.Fatalf("%s: %+v %+v", post.id, r, r.Error)
			}
			if from := seenFrom(t, watcher, post.text); from != post.nick {
				t.Fatalf("%s authored as %q", post.id, from)
			}
		}
	})

	t.Run("zeromq", func(t *testing.T) {
		conn := dialZMTPRequest(t, g.addresses["zmq"])
		// A heartbeat between messages is answered, and the connection
		// stays usable after idling beyond the old read deadline.
		zmtpPing(t, conn)
		time.Sleep(3500 * time.Millisecond)
		reply := zmtpCall(t, conn, mustBody(t, sendRequest("zmq-guest", "#lobby", "guest over ZeroMQ", "zmq-guest", "")))
		if reply.Error != nil || reply.Result == nil || reply.RequestID != "zmq-guest" {
			t.Fatalf("ZeroMQ guest: %+v", reply)
		}
		if from := seenFrom(t, watcher, "guest over ZeroMQ"); from != "zmq-guest" {
			t.Fatal("ZeroMQ guest authored as", from)
		}
	})

	t.Run("nng", func(t *testing.T) {
		conn := dialNNGRequest(t, g.addresses["nng"])
		time.Sleep(3500 * time.Millisecond)
		reply := nngCall(t, conn, 0x80000001, mustBody(t, sendRequest("nng-alice", "#lobby", "alice over NNG", "alice", aliceToken)))
		if reply.Error != nil || reply.Result == nil {
			t.Fatalf("NNG registered post: %+v", reply)
		}
		if from := seenFrom(t, watcher, "alice over NNG"); from != "alice" {
			t.Fatal("NNG post authored as", from)
		}
	})

	t.Run("mqtt-sn", func(t *testing.T) {
		address, err := net.ResolveUDPAddr("udp", g.addresses["sn"])
		if err != nil {
			t.Fatal(err)
		}
		client, err := net.DialUDP("udp", nil, address)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		mqttSNSend(t, client, mqttSNPublication(7, string(mustBody(t, sendRequest("sn-guest", "#lobby", "guest over MQTT-SN", "sn-guest", ""))), false))
		if from := seenFrom(t, watcher, "guest over MQTT-SN"); from != "sn-guest" {
			t.Fatal("MQTT-SN post authored as", from)
		}
	})
}

type localREPReply struct {
	RequestID string          `json:"request_id"`
	Result    json.RawMessage `json:"result"`
	Error     *core.Failure   `json:"error"`
}

// dialZMTPRequest is an independent ZMTP 3.1 REQ peer (NULL mechanism).
func dialZMTPRequest(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	greeting := make([]byte, 64)
	greeting[0], greeting[9], greeting[10], greeting[11] = 0xff, 0x7f, 3, 1
	copy(greeting[12:], "NULL")
	ready := append([]byte{5}, "READY"...)
	ready = append(append(ready, 11), "Socket-Type"...)
	ready = append(binary.BigEndian.AppendUint32(ready, 3), "REQ"...)
	if _, err := conn.Write(append(greeting, append([]byte{4, byte(len(ready))}, ready...)...)); err != nil {
		t.Fatal(err)
	}
	peer := make([]byte, 64)
	if _, err := io.ReadFull(conn, peer); err != nil || peer[0] != 0xff || peer[10] != 3 {
		t.Fatalf("ZMTP greeting %x %v", peer, err)
	}
	if command := readZMTPFrame(t, conn); string(command[1:6]) != "READY" {
		t.Fatalf("ZMTP READY %q", command)
	}
	return conn
}

func readZMTPFrame(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	var header [9]byte
	if _, err := io.ReadFull(conn, header[:2]); err != nil {
		t.Fatal(err)
	}
	size := uint64(header[1])
	if header[0]&2 != 0 {
		if _, err := io.ReadFull(conn, header[2:9]); err != nil {
			t.Fatal(err)
		}
		size = binary.BigEndian.Uint64(header[1:9])
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	return body
}

func zmtpPing(t *testing.T, conn net.Conn) {
	t.Helper()
	ping := append([]byte("\x04PING\x00\x00"), "hb"...)
	if _, err := conn.Write(append([]byte{4, byte(len(ping))}, ping...)); err != nil {
		t.Fatal(err)
	}
	if pong := readZMTPFrame(t, conn); string(pong) != "\x04PONGhb" {
		t.Fatalf("PONG %q", pong)
	}
}

func zmtpCall(t *testing.T, conn net.Conn, body []byte) localREPReply {
	t.Helper()
	wire := []byte{1, 0}
	if len(body) < 256 {
		wire = append(wire, 0, byte(len(body)))
	} else {
		wire = binary.BigEndian.AppendUint64(append(wire, 2), uint64(len(body)))
	}
	if _, err := conn.Write(append(wire, body...)); err != nil {
		t.Fatal(err)
	}
	if delimiter := readZMTPFrame(t, conn); len(delimiter) != 0 {
		t.Fatalf("REP delimiter %q", delimiter)
	}
	var reply localREPReply
	if data := readZMTPFrame(t, conn); json.Unmarshal(data, &reply) != nil {
		t.Fatalf("REP body %q", data)
	}
	return reply
}

// dialNNGRequest is an independent SP REQ0 peer over TCP.
func dialNNGRequest(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte{0, 'S', 'P', 0, 0, 0x30, 0, 0}); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 8)
	if _, err := io.ReadFull(conn, header); err != nil || header[5] != 0x31 {
		t.Fatalf("SP header %x %v", header, err)
	}
	return conn
}

func nngCall(t *testing.T, conn net.Conn, id uint32, body []byte) localREPReply {
	t.Helper()
	wire := binary.BigEndian.AppendUint64(nil, uint64(len(body)+4))
	wire = binary.BigEndian.AppendUint32(wire, id)
	if _, err := conn.Write(append(wire, body...)); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 12)
	if _, err := io.ReadFull(conn, header); err != nil || binary.BigEndian.Uint32(header[8:]) != id {
		t.Fatalf("NNG reply header %x %v", header, err)
	}
	data := make([]byte, binary.BigEndian.Uint64(header[:8])-4)
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatal(err)
	}
	var reply localREPReply
	if json.Unmarshal(data, &reply) != nil {
		t.Fatalf("NNG reply %q", data)
	}
	return reply
}
