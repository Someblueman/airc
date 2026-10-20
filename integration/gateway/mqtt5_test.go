//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
)

// mqtt5Client is a minimal MQTT 5 client written independently of the
// gateway's codec: just enough to connect with user properties, subscribe,
// publish requests with a response topic, and acknowledge deliveries.
type mqtt5Client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	id   uint16
	// held are packets read while waiting for another type.
	held []*mqtt5Packet
}

type mqtt5Packet struct {
	kind    byte // the high nibble of the fixed header
	flags   byte
	body    []byte
	topic   string
	id      uint16
	reason  byte
	payload []byte
	props   map[byte][]byte
	user    map[string]string
}

func mqttVarint(b []byte, v int) []byte {
	for {
		d := byte(v % 128)
		v /= 128
		if v > 0 {
			d |= 128
		}
		b = append(b, d)
		if v == 0 {
			return b
		}
	}
}

// mqttProps encodes properties from (id, value) pairs: a string value is a
// UTF-8 string (or, for 0x26, "key=value"), a uint32 four bytes, a []byte
// binary data.
func mqttProps(pairs ...any) []byte {
	var body []byte
	for i := 0; i < len(pairs); i += 2 {
		id := pairs[i].(byte)
		body = append(body, id)
		switch v := pairs[i+1].(type) {
		case string:
			if id == 0x26 {
				key, value, _ := strings.Cut(v, "=")
				body = mqttString(mqttString(body, key), value)
			} else {
				body = mqttString(body, v)
			}
		case uint32:
			body = binary.BigEndian.AppendUint32(body, v)
		case []byte:
			body = binary.BigEndian.AppendUint16(body, uint16(len(v)))
			body = append(body, v...)
		}
	}
	return append(mqttVarint(nil, len(body)), body...)
}

func (c *mqtt5Client) send(header byte, body []byte) {
	c.t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write(append(mqttVarint([]byte{header}, len(body)), body...)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *mqtt5Client) read() *mqtt5Packet {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	header, err := c.r.ReadByte()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		c.t.Fatal(err)
	}
	size, shift := 0, 0
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			c.t.Fatal(err)
		}
		size |= int(b&127) << shift
		shift += 7
		if b&128 == 0 {
			break
		}
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(c.r, body); err != nil {
		c.t.Fatal(err)
	}
	p := &mqtt5Packet{kind: header >> 4, flags: header & 15, body: body, props: map[byte][]byte{}, user: map[string]string{}}
	pos := 0
	u16 := func() uint16 { v := binary.BigEndian.Uint16(body[pos:]); pos += 2; return v }
	str := func() string { n := int(u16()); s := string(body[pos : pos+n]); pos += n; return s }
	props := func() {
		n, shift := 0, 0
		for {
			b := body[pos]
			pos++
			n |= int(b&127) << shift
			shift += 7
			if b&128 == 0 {
				break
			}
		}
		end := pos + n
		for pos < end {
			id := body[pos]
			pos++
			switch id {
			case 0x01, 0x24, 0x25, 0x28, 0x29, 0x2A:
				p.props[id] = body[pos : pos+1]
				pos++
			case 0x13, 0x21, 0x22:
				p.props[id] = body[pos : pos+2]
				pos += 2
			case 0x02, 0x11, 0x27:
				p.props[id] = body[pos : pos+4]
				pos += 4
			case 0x26:
				k := str()
				p.user[k] = str()
			default: // strings and binary data
				p.props[id] = []byte(str())
			}
		}
	}
	switch p.kind {
	case 2: // CONNACK
		p.reason = body[1]
		pos = 2
		props()
	case 3: // PUBLISH
		p.topic = str()
		if p.flags&6 != 0 {
			p.id = u16()
		}
		props()
		p.payload = body[pos:]
	case 4: // PUBACK
		p.id = u16()
		if len(body) > 2 {
			p.reason = body[2]
			pos = 3
		}
		if len(body) > 3 {
			props()
		}
	case 9, 11: // SUBACK, UNSUBACK: reason codes after the properties
		p.id = u16()
		props()
		p.payload = body[pos:]
	case 14: // DISCONNECT
		if len(body) > 0 {
			p.reason = body[0]
		}
	}
	return p
}

// next returns the next packet of kind, keeping others for a later call
// and dropping PINGRESPs.
func (c *mqtt5Client) next(kind byte) *mqtt5Packet {
	c.t.Helper()
	for i, p := range c.held {
		if p.kind == kind {
			c.held = append(c.held[:i], c.held[i+1:]...)
			return p
		}
	}
	for {
		p := c.read()
		if p == nil {
			c.t.Fatalf("connection closed waiting for packet type %d", kind)
		}
		if p.kind == kind {
			return p
		}
		if p.kind == 14 {
			c.t.Fatalf("server disconnected with reason %#x waiting for packet type %d", p.reason, kind)
		}
		if p.kind != 13 {
			c.held = append(c.held, p)
		}
	}
}

func dialMQTT5(t *testing.T, address, clientID string, cleanStart bool, expiry uint32, user ...string) (*mqtt5Client, *mqtt5Packet) {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	return connectMQTT5(t, conn, clientID, cleanStart, expiry, nil, user...)
}

// mqttWill is a CONNECT's will; delay is its Will Delay Interval.
type mqttWill struct {
	topic, payload string
	delay          uint32
	retain         bool
}

// connectMQTT5 sends CONNECT on conn and reads the CONNACK.
func connectMQTT5(t *testing.T, conn net.Conn, clientID string, cleanStart bool, expiry uint32, will *mqttWill, user ...string) (*mqtt5Client, *mqtt5Packet) {
	t.Helper()
	t.Cleanup(func() { _ = conn.Close() })
	c := &mqtt5Client{t: t, conn: conn, r: bufio.NewReader(conn)}
	flags := byte(0)
	if cleanStart {
		flags |= 2
	}
	if will != nil {
		flags |= 4
		if will.retain {
			flags |= 0x20
		}
	}
	body := mqttString(nil, "MQTT")
	body = append(body, 5, flags, 0, 30)
	pairs := []any{byte(0x11), expiry}
	for _, kv := range user {
		pairs = append(pairs, byte(0x26), kv)
	}
	body = append(body, mqttProps(pairs...)...)
	body = mqttString(body, clientID)
	if will != nil {
		body = append(body, mqttProps(byte(0x18), will.delay)...)
		body = mqttString(body, will.topic)
		body = mqttString(body, will.payload)
	}
	c.send(0x10, body)
	return c, c.next(2)
}

func (c *mqtt5Client) subscribe(filter string, qos byte, user ...string) []byte {
	c.t.Helper()
	c.id++
	body := binary.BigEndian.AppendUint16(nil, c.id)
	var pairs []any
	for _, kv := range user {
		pairs = append(pairs, byte(0x26), kv)
	}
	body = append(body, mqttProps(pairs...)...)
	body = append(mqttString(body, filter), qos)
	c.send(0x82, body)
	return c.next(9).payload
}

func (c *mqtt5Client) publish(topic string, qos byte, payload string, props ...any) *mqtt5Packet {
	c.t.Helper()
	body := mqttString(nil, topic)
	if qos > 0 {
		c.id++
		body = binary.BigEndian.AppendUint16(body, c.id)
	}
	body = append(body, mqttProps(props...)...)
	body = append(body, payload...)
	c.send(0x30|qos<<1, body)
	if qos == 0 {
		return nil
	}
	return c.next(4)
}

func (c *mqtt5Client) ack(id uint16) {
	c.t.Helper()
	c.send(0x40, binary.BigEndian.AppendUint16(nil, id))
}

func mqttRequest(nick, operation, room, message string) string {
	fields := map[string]any{"version": 1, "request_id": fmt.Sprintf("m5-%d", time.Now().UnixNano()), "operation": operation, "author": map[string]string{"nick": nick}}
	if room != "" {
		fields["target"] = map[string]string{"kind": "room", "name": room}
	}
	if message != "" {
		fields["message"] = message
	}
	if operation == "history.read" {
		fields["limit"] = 10
	}
	data, _ := json.Marshal(fields)
	return string(data)
}

// MQTT 5 on the MQTT listener: a request with a Response Topic gets its
// result there and a PUBACK reason code; a refused request is a reason
// code, not a closed connection; a room subscription delivers posts at QoS
// 1; a session that resumes gets what it missed while away, and an
// unacknowledged message again, and a taken-over connection is told so.
func TestMQTT5RequestsSubscriptionsAndSessions(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "mqtt", Protocol: "mqtt"})
	g := startAuthorGateway(t, c)
	address := g.addresses["mqtt"]
	poster := dial(t, socket, "m5-poster", "", false, true)
	post := func(room, text string) string {
		t.Helper()
		if err := poster.Send(room, text); err != nil {
			t.Fatal(err)
		}
		return next(t, poster, func(e *irc.SendReceiptEvent) bool { return e.Message == text }).ID
	}

	// CONNACK advertises the profile.
	client, connack := dialMQTT5(t, address, "m5-reader", true, 600, "airc-nick=m5-reader")
	if connack.reason != 0 || connack.flags != 0 || connack.body[0] != 0 {
		t.Fatalf("CONNACK reason %#x, session present %d", connack.reason, connack.body[0])
	}
	if string(connack.props[0x24]) != "\x01" || string(connack.props[0x25]) != "\x00" || string(connack.props[0x28]) != "\x00" {
		t.Fatalf("CONNACK properties %v", connack.props)
	}

	// Request/response: subscribe to a response topic, publish a read.
	if got := client.subscribe("replies/m5-reader", 1); len(got) != 1 || got[0] != 1 {
		t.Fatalf("response topic SUBACK %v", got)
	}
	first := post("#m5", "before subscribing")
	puback := client.publish("airc/requests", 1, mqttRequest("m5-reader", "history.read", "#m5", ""), byte(0x08), "replies/m5-reader", byte(0x09), []byte("corr-1"))
	if puback.reason != 0 {
		t.Fatalf("read PUBACK %#x %q", puback.reason, puback.props[0x1F])
	}
	reply := client.next(3)
	if reply.topic != "replies/m5-reader" || string(reply.props[0x09]) != "corr-1" || !strings.Contains(string(reply.payload), first) {
		t.Fatalf("reply on %q corr %q: %s", reply.topic, reply.props[0x09], reply.payload)
	}
	client.ack(reply.id)
	// A refusal is a reason code with the gateway's code, and the
	// connection stays up.
	refused := client.publish("airc/requests", 1, `{"version":1}`)
	if refused.reason != 0x99 {
		t.Fatalf("an invalid request: PUBACK %#x %q", refused.reason, refused.props[0x1F])
	}

	// A room subscription starts after the latest message and delivers
	// each post as it lands.
	if got := client.subscribe("airc/room/m5", 1); len(got) != 1 || got[0] != 1 {
		t.Fatalf("room SUBACK %v", got)
	}
	live := post("#m5", "live")
	delivered := client.next(3)
	var m irc.MessageMetadata
	if err := json.Unmarshal(delivered.payload, &m); err != nil || delivered.topic != "airc/room/m5" || m.ID != live || delivered.flags&6 != 2 {
		t.Fatalf("delivery on %q flags %d: %s %v", delivered.topic, delivered.flags, delivered.payload, err)
	}
	client.ack(delivered.id)

	// A wildcard is refused; an unauthenticated session cannot follow rooms.
	if got := client.subscribe("airc/room/+", 1); got[0] != 0xA2 {
		t.Fatalf("wildcard SUBACK %v", got)
	}
	anonymous, _ := dialMQTT5(t, address, "m5-anonymous", true, 600)
	if got := anonymous.subscribe("airc/room/m5", 1); got[0] != 0x87 {
		t.Fatalf("anonymous room SUBACK %v", got)
	}
	if got := anonymous.subscribe("replies/anonymous", 1); got[0] != 1 {
		t.Fatalf("anonymous response topic SUBACK %v", got)
	}
	// A session without an author is never resumed: a client ID proves
	// nothing.
	_ = anonymous.conn.Close()
	if _, connack := dialMQTT5(t, address, "m5-anonymous", false, 600); connack.body[0] != 0 {
		t.Fatal("an anonymous session was resumed")
	}

	// Away: one message is delivered but not acknowledged, then the link
	// drops; two more arrive while the session is away.
	unacked := post("#m5", "unacknowledged")
	pending := client.next(3)
	if !strings.Contains(string(pending.payload), unacked) {
		t.Fatalf("pending delivery %s", pending.payload)
	}
	_ = client.conn.Close()
	missed1, missed2 := post("#m5", "while away 1"), post("#m5", "while away 2")

	// Resuming: Session Present, the unacknowledged message again with
	// DUP and its original ID, then what was missed, in order.
	resumed, connack := dialMQTT5(t, address, "m5-reader", false, 600, "airc-nick=m5-reader")
	if connack.reason != 0 || connack.body[0] != 1 {
		t.Fatalf("resume CONNACK reason %#x present %d", connack.reason, connack.body[0])
	}
	again := resumed.next(3)
	if again.id != pending.id || again.flags&8 == 0 || !strings.Contains(string(again.payload), unacked) {
		t.Fatalf("redelivery id %d (want %d) flags %d: %s", again.id, pending.id, again.flags, again.payload)
	}
	resumed.ack(again.id)
	for _, want := range []string{missed1, missed2} {
		got := resumed.next(3)
		if !strings.Contains(string(got.payload), want) {
			t.Fatalf("missed message: %s, want %s", got.payload, want)
		}
		resumed.ack(got.id)
	}

	// Another author, or a client without one, cannot take the client ID:
	// it is refused, and the connection holding it carries on.
	for _, user := range [][]string{{"airc-nick=m5-intruder"}, nil} {
		for _, clean := range []bool{false, true} {
			if _, connack := dialMQTT5(t, address, "m5-reader", clean, 600, user...); connack.reason != 0x87 {
				t.Fatalf("%v clean %v took the client ID: CONNACK %#x", user, clean, connack.reason)
			}
		}
	}
	still := post("#m5", "still here")
	if got := resumed.next(3); !strings.Contains(string(got.payload), still) {
		t.Fatalf("after refused takeovers: %s", got.payload)
	} else {
		resumed.ack(got.id)
	}
	// The same author replaces it, and the connection it replaced is told
	// why.
	if _, connack := dialMQTT5(t, address, "m5-reader", true, 600, "airc-nick=m5-reader"); connack.reason != 0 {
		t.Fatalf("the owner's takeover CONNACK %#x", connack.reason)
	}
	if d := resumed.next(14); d.reason != 0x8E {
		t.Fatalf("takeover DISCONNECT %#x", d.reason)
	}
}

// The listener's session caps hold: beyond sessions_per_author one author
// is refused, and beyond sessions everyone is.
func TestMQTT5SessionCaps(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "mqtt", Protocol: "mqtt", MQTT: &config.MQTT{Sessions: 3, SessionsPerAuthor: 2}})
	g := startAuthorGateway(t, c)
	address := g.addresses["mqtt"]
	for i, want := range []byte{0, 0, 0x97} {
		if _, connack := dialMQTT5(t, address, fmt.Sprintf("m9-a%d", i), true, 600, "airc-nick=m9-alice"); connack.reason != want {
			t.Fatalf("alice's session %d: CONNACK %#x, want %#x", i, connack.reason, want)
		}
	}
	for i, want := range []byte{0, 0x97} {
		if _, connack := dialMQTT5(t, address, fmt.Sprintf("m9-b%d", i), true, 600, "airc-nick=m9-bob"); connack.reason != want {
			t.Fatalf("bob's session %d: CONNACK %#x, want %#x", i, connack.reason, want)
		}
	}
}

// mqtt5Connections counts the MQTT 5 connections the in-process gateway is
// still serving, its leaving of shared groups and scheduling of wills
// included, so tests can wait for it to finish with a closed one.
func mqtt5Connections() int {
	n := 0
	for line := range strings.Lines(string(goroutineStacks())) {
		if strings.HasPrefix(line, "github.com/Someblueman/airc/internal/gateway/adapter.serveMQTT5(") {
			n++
		}
	}
	return n
}

// drainMQTT5 reads PUBLISHes on c, acknowledging each, until none arrives
// for quiet, and returns their message bodies.
func drainMQTT5(t *testing.T, c *mqtt5Client, quiet time.Duration) []string {
	t.Helper()
	var bodies []string
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(quiet))
		header, err := c.r.ReadByte()
		if err != nil {
			return bodies
		}
		size, shift := 0, 0
		for {
			b, err := c.r.ReadByte()
			if err != nil {
				t.Fatal(err)
			}
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		packet := make([]byte, size)
		if _, err := io.ReadFull(c.r, packet); err != nil {
			t.Fatal(err)
		}
		if header>>4 != 3 {
			continue
		}
		pos := 2 + int(binary.BigEndian.Uint16(packet))
		if header>>1&3 > 0 {
			c.ack(binary.BigEndian.Uint16(packet[pos:]))
			pos += 2
		}
		propLen, n := 0, 0
		for shift := 0; ; shift += 7 {
			b := packet[pos+n]
			n++
			propLen |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
		}
		var m struct {
			Body string `json:"body_base64"`
		}
		if err := json.Unmarshal(packet[pos+n+propLen:], &m); err != nil {
			t.Fatal(err)
		}
		body, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(m.Body, "="))
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(body))
	}
}

// Shared subscriptions: the members of one author's group each get some of
// a room's messages and together every one, once; a plain subscription
// and another author's group of the same name each get them all; a member
// that leaves hands its share to the rest, and one that resumes its
// session rejoins.
func TestMQTT5SharedSubscriptions(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "mqtt", Protocol: "mqtt"})
	g := startAuthorGateway(t, c)
	address := g.addresses["mqtt"]
	poster := dial(t, socket, "m10-poster", "", false, true)
	post := func(prefix string, n int) []string {
		t.Helper()
		var sent []string
		for i := range n {
			text := fmt.Sprintf("%s %d", prefix, i)
			if err := poster.Send("#m10", text); err != nil {
				t.Fatal(err)
			}
			next(t, poster, func(e *irc.SendReceiptEvent) bool { return e.Message == text })
			sent = append(sent, text)
		}
		return sent
	}
	member := func(id string) *mqtt5Client {
		client, connack := dialMQTT5(t, address, id, true, 600, "airc-nick=m10-pool")
		if connack.reason != 0 || connack.props[0x2A] == nil || connack.props[0x2A][0] != 1 {
			t.Fatalf("%s CONNACK %#x, shared subscriptions available %v", id, connack.reason, connack.props[0x2A])
		}
		if got := client.subscribe("$share/pool/airc/room/m10", 1); got[0] != 1 {
			t.Fatalf("%s SUBACK %v", id, got)
		}
		return client
	}
	w1, w2 := member("m10-w1"), member("m10-w2")
	watcher, _ := dialMQTT5(t, address, "m10-watcher", true, 600, "airc-nick=m10-watcher")
	if got := watcher.subscribe("airc/room/m10", 1); got[0] != 1 {
		t.Fatalf("watcher SUBACK %v", got)
	}
	other, _ := dialMQTT5(t, address, "m10-other", true, 600, "airc-nick=m10-other")
	if got := other.subscribe("$share/pool/airc/room/m10", 1); got[0] != 1 {
		t.Fatalf("other author's SUBACK %v", got)
	}
	for filter, want := range map[string]byte{"$share//airc/room/m10": 0x8F, "$share/pool/replies/x": 0x9E, "$share/pool": 0x8F} {
		if got := watcher.subscribe(filter, 1); got[0] != want {
			t.Fatalf("SUBACK for %s: %#x, want %#x", filter, got[0], want)
		}
	}

	sent := post("pooled", 20)
	quiet := 1500 * time.Millisecond
	got1, got2 := drainMQTT5(t, w1, quiet), drainMQTT5(t, w2, quiet)
	if len(got1) == 0 || len(got2) == 0 {
		t.Fatalf("one member got nothing: %d and %d", len(got1), len(got2))
	}
	shared := append(append([]string(nil), got1...), got2...)
	slices.Sort(shared)
	want := slices.Clone(sent)
	slices.Sort(want)
	if !slices.Equal(shared, want) {
		t.Fatalf("the group got %v, want each of %v once", shared, want)
	}
	for name, client := range map[string]*mqtt5Client{"watcher": watcher, "other author's group": other} {
		if got := drainMQTT5(t, client, quiet); !slices.Equal(got, sent) {
			t.Fatalf("%s got %v, want all %d in order", name, got, len(sent))
		}
	}

	// w1 leaves: once the gateway has ended its connection, its share goes
	// to w2. w1 comes back to its session.
	serving := mqtt5Connections()
	_ = w1.conn.Close()
	testwait.Until(t, 5*time.Second, "the gateway to end w1's connection", func() bool { return mqtt5Connections() < serving })
	alone := post("alone", 6)
	if got := drainMQTT5(t, w2, quiet); !slices.Equal(got, alone) {
		t.Fatalf("the remaining member got %v, want %v", got, alone)
	}
	back, connack := dialMQTT5(t, address, "m10-w1", false, 600, "airc-nick=m10-pool")
	if connack.reason != 0 || connack.body[0] != 1 {
		t.Fatalf("resume CONNACK %#x present %d", connack.reason, connack.body[0])
	}
	again := post("again", 10)
	got1, got2 = drainMQTT5(t, back, quiet), drainMQTT5(t, w2, quiet)
	if len(got1) == 0 || len(got1)+len(got2) != len(again) {
		t.Fatalf("after w1 resumed: %d and %d of %d", len(got1), len(got2), len(again))
	}
}
