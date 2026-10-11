//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapOptions "github.com/plgd-dev/go-coap/v3/options"
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	wt "github.com/quic-go/webtransport-go"
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

// receiveAGD1 reads AGD1 fragments from recv until one response assembles.
func receiveAGD1(t *testing.T, recv func() []byte) []byte {
	t.Helper()
	byIndex := map[int][]byte{}
	for {
		fragment := recv()
		if len(fragment) < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" {
			t.Fatalf("not an AGD1 datagram: %q", fragment)
		}
		byIndex[int(binary.BigEndian.Uint16(fragment[24:26]))] = fragment
		if count := int(binary.BigEndian.Uint16(fragment[26:28])); len(byIndex) == count {
			fragments := make([][]byte, 0, count)
			for _, f := range byIndex {
				fragments = append(fragments, f)
			}
			return assembleResponseDatagrams(t, fragments)
		}
	}
}

func writeLengthFrame(t *testing.T, w io.Writer, frame []byte) {
	t.Helper()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if _, err := w.Write(append(header[:], frame...)); err != nil {
		t.Fatal(err)
	}
}

func readLengthFrame(t *testing.T, r io.Reader) []byte {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatal(err)
	}
	return body
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

	certs := map[string]testtls.Certificate{}
	tlsListener := func(id, protocol string, udp bool) config.Listener {
		cert := testtls.NewP256(t)
		certs[id] = cert
		l := config.Listener{ID: id, Protocol: protocol, TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath}
		if udp {
			l.Address = freeUDP(t)
		}
		return l
	}
	lobby := core.Target{Kind: "room", Name: "#lobby"}
	c := authorConfig(t, socket, gatewayToken,
		config.Listener{ID: "web", Protocol: "http"},
		tlsListener("quic", "rawquic", true),
		tlsListener("quicdg", "rawquic_datagrams", true),
		tlsListener("h3", "http3", true),
		tlsListener("coaps", "coaps", true),
		tlsListener("wt", "webtransport", true),
		tlsListener("rtc", "webrtc", false),
		config.Listener{ID: "coap", Protocol: "coap_udp", Address: freeUDP(t)},
		config.Listener{ID: "coaptcp", Protocol: "coap_tcp"},
		config.Listener{ID: "udp", Protocol: "udp", Address: freeUDP(t)},
		config.Listener{ID: "zmq", Protocol: "zeromq"},
		config.Listener{ID: "nng", Protocol: "nng"},
		config.Listener{ID: "sn", Protocol: "mqtt_sn", Address: freeUDP(t), Operations: []core.Operation{core.Send}, MQTTSN: &config.MQTTSN{Routes: []config.MQTTSNRoute{{TopicID: 7, Target: lobby}}}},
	)
	g := startAuthorGateway(t, c)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	t.Run("quic-streams-identify", func(t *testing.T) {
		tlsConfig := certs["quic"].Client.Clone()
		tlsConfig.NextProtos = []string{adapter.QUICALPN}
		conn, err := quic.DialAddr(ctx, g.addresses["quic"], tlsConfig, &quic.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseWithError(0, "done")
		call := func(id string, request map[string]any) plainFrameResponse {
			stream, err := conn.OpenStreamSync(ctx)
			if err != nil {
				t.Fatal(err)
			}
			writeLengthFrame(t, stream, plainFrameBytes(t, id, request))
			response := decodePlainFrame(t, id, readLengthFrame(t, stream))
			_ = stream.Close()
			return response
		}
		if r := call("guest", sendRequest("quic-guest", "#lobby", "guest over QUIC", "quic-guest", "")); r.Event != "result" {
			t.Fatalf("guest: %+v %+v", r, r.Error)
		}
		if from := seenFrom(t, watcher, "guest over QUIC"); from != "quic-guest" {
			t.Fatal("QUIC guest authored as", from)
		}
		// Identify on one stream; another stream of the connection posts as
		// the account without naming it.
		if r := call("identify", plainRequest(map[string]any{"request_id": "quic-identify", "operation": "account.identify"}, "alice", aliceToken)); r.Event != "result" || r.Result.Account == nil {
			t.Fatalf("identify: %+v %+v", r, r.Error)
		}
		if r := call("post", sendRequest("quic-identified", "#lobby", "identified over QUIC", "", "")); r.Event != "result" {
			t.Fatalf("identified post: %+v %+v", r, r.Error)
		}
		if from := seenFrom(t, watcher, "identified over QUIC"); from != "alice" {
			t.Fatal("QUIC identified post authored as", from)
		}
	})

	t.Run("quic-datagrams", func(t *testing.T) {
		tlsConfig := certs["quicdg"].Client.Clone()
		tlsConfig.NextProtos = []string{adapter.QUICALPN}
		conn, err := quic.DialAddr(ctx, g.addresses["quicdg"], tlsConfig, &quic.Config{EnableDatagrams: true})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseWithError(0, "done")
		for _, fragment := range agd1(t, plainFrameBytes(t, "dg", sendRequest("quic-datagram", "#lobby", "guest over QUIC datagrams", "dgram-guest", ""))) {
			if err := conn.SendDatagram(fragment); err != nil {
				t.Fatal(err)
			}
		}
		reply := receiveAGD1(t, func() []byte {
			data, err := conn.ReceiveDatagram(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return data
		})
		if r := decodePlainFrame(t, "dg", reply); r.Event != "result" {
			t.Fatalf("datagram: %+v %+v", r, r.Error)
		}
		if from := seenFrom(t, watcher, "guest over QUIC datagrams"); from != "dgram-guest" {
			t.Fatal("QUIC datagram post authored as", from)
		}
	})

	t.Run("webtransport", func(t *testing.T) {
		tlsConfig := certs["wt"].Client.Clone()
		tlsConfig.NextProtos = []string{"h3"}
		transport := &wt.Transport{TLSClientConfig: tlsConfig, ApplicationProtocols: []string{"airc-gateway.v1"}, QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true}}
		defer transport.Close()
		_, session, err := transport.Dial(ctx, "https://"+g.addresses["wt"]+"/v1/webtransport", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.CloseWithError(0, "done")
		// Identify on a bidirectional stream; the session's uni streams and
		// datagrams then post as the account.
		stream, err := session.OpenStreamSync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		writeLengthFrame(t, stream, plainFrameBytes(t, "identify", plainRequest(map[string]any{"request_id": "wt-identify", "operation": "account.identify"}, "alice", aliceToken)))
		if r := decodePlainFrame(t, "identify", readLengthFrame(t, stream)); r.Event != "result" || r.Result.Account == nil {
			t.Fatalf("WebTransport identify: %+v %+v", r, r.Error)
		}
		_ = stream.Close()
		uni, err := session.OpenUniStreamSync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		writeLengthFrame(t, uni, plainFrameBytes(t, "uni", sendRequest("wt-uni", "#lobby", "alice over WebTransport uni", "", "")))
		reply, err := session.AcceptUniStream(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if r := decodePlainFrame(t, "uni", readLengthFrame(t, reply)); r.Event != "result" {
			t.Fatalf("WebTransport uni: %+v %+v", r, r.Error)
		}
		if from := seenFrom(t, watcher, "alice over WebTransport uni"); from != "alice" {
			t.Fatal("WebTransport uni post authored as", from)
		}
		for _, fragment := range agd1(t, plainFrameBytes(t, "dg", sendRequest("wt-dg", "#lobby", "guest over WebTransport datagrams", "wt-guest", ""))) {
			if err := session.SendDatagram(fragment); err != nil {
				t.Fatal(err)
			}
		}
		data := receiveAGD1(t, func() []byte {
			data, err := session.ReceiveDatagram(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return data
		})
		if r := decodePlainFrame(t, "dg", data); r.Event != "result" {
			t.Fatalf("WebTransport datagram: %+v %+v", r, r.Error)
		}
		if from := seenFrom(t, watcher, "guest over WebTransport datagrams"); from != "wt-guest" {
			t.Fatal("WebTransport datagram post authored as", from)
		}
	})

	t.Run("webrtc", func(t *testing.T) {
		peer := webRTCFixture{address: g.addresses["rtc"], cert: certs["rtc"]}
		for _, mode := range []string{"reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"} {
			session := newWebRTCSession(t, peer, mode)
			text := "guest over WebRTC " + mode
			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				t.Fatal(err)
			}
			webRTCSessionSendBody(t, session, plainFrameBytes(t, "rtc", sendRequest("rtc-"+mode, "#lobby", text, "rtc-guest", "")), id)
			if r := webRTCReceiveFrame(t, session); r.Event != "result" {
				t.Fatalf("WebRTC %s: %+v %+v", mode, r, r.Error)
			}
			if from := seenFrom(t, watcher, text); from != "rtc-guest" {
				t.Fatalf("WebRTC %s post authored as %q", mode, from)
			}
			_ = session.pc.Close()
		}
	})

	t.Run("http3", func(t *testing.T) {
		transport := &http3.Transport{TLSClientConfig: certs["h3"].Client.Clone()}
		defer transport.Close()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+g.addresses["h3"]+"/v1/messages", bytes.NewReader(mustBody(t, sendRequest("h3-alice", "#lobby", "alice over HTTP/3", "alice", aliceToken))))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Transport: transport}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatal("HTTP/3 post", response.StatusCode)
		}
		if from := seenFrom(t, watcher, "alice over HTTP/3"); from != "alice" {
			t.Fatal("HTTP/3 post authored as", from)
		}
	})

	coapPost := func(t *testing.T, post func(context.Context, string, message.MediaType, io.ReadSeeker) ([]byte, error), id, text, nick, token string) {
		t.Helper()
		body, err := post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(plainFrameBytes(t, id, sendRequest(id, "#lobby", text, nick, token))))
		if err != nil {
			t.Fatal(err)
		}
		if r := decodePlainFrame(t, id, body); r.Event != "result" {
			t.Fatalf("%s: %+v %+v", id, r, r.Error)
		}
		if from := seenFrom(t, watcher, text); from != nick {
			t.Fatalf("%s authored as %q", id, from)
		}
	}

	t.Run("coap-udp", func(t *testing.T) {
		client, err := coapUDP.Dial(g.addresses["coap"], coapOptions.WithBlockwise(true, blockwise.SZX1024, 5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		post := func(ctx context.Context, path string, media message.MediaType, body io.ReadSeeker) ([]byte, error) {
			response, err := client.Post(ctx, path, media, body)
			if err != nil {
				return nil, err
			}
			return response.ReadBody()
		}
		coapPost(t, post, "coap-guest", "guest over CoAP", "coap-guest", "")
		coapPost(t, post, "coap-alice", "alice over CoAP", "alice", aliceToken)
	})

	t.Run("coap-tcp", func(t *testing.T) {
		client := dialPlainCoAPTCP(t, g.addresses["coaptcp"])
		coapPost(t, func(ctx context.Context, path string, media message.MediaType, body io.ReadSeeker) ([]byte, error) {
			response, err := client.Post(ctx, path, media, body)
			if err != nil {
				return nil, err
			}
			return response.ReadBody()
		}, "coaptcp-guest", "guest over CoAP TCP", "coaptcp-guest", "")
	})

	t.Run("coaps-dtls", func(t *testing.T) {
		client := dialCoAPS(t, g.addresses["coaps"], certs["coaps"])
		coapPost(t, func(ctx context.Context, path string, media message.MediaType, body io.ReadSeeker) ([]byte, error) {
			response, err := client.Post(ctx, path, media, body)
			if err != nil {
				return nil, err
			}
			return response.ReadBody()
		}, "coaps-guest", "guest over CoAPS", "coaps-guest", "")
	})

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
