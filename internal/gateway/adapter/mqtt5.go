package adapter

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
	"github.com/Someblueman/airc/internal/protocol"
)

// MQTT 5 on the same listener as 3.1.1 adds what a flaky link needs:
//
//   - Sessions that outlive a connection. With Clean Start 0 and a Session
//     Expiry Interval (at most mqtt5MaxSessionExpiry), a client that comes
//     back finds its subscriptions and the position it reached in each, and
//     unacknowledged QoS 1 messages are sent again. Sessions live in the
//     gateway's memory: a gateway restart loses them, and CONNACK's Session
//     Present says so.
//   - Subscriptions to rooms (airc/room/<name> for #<name>, or the &name
//     itself), followed threads (airc/thread/<id>) and the author's inbox
//     (airc/inbox), carried by one followed check per connection. A message
//     is published at the subscription's QoS (at most 1) as the message's
//     JSON; a session that was away gets what it missed from history,
//     within retention, and a user property airc-gap marks a retention gap.
//   - Request/response. A request published with a Response Topic may be any
//     operation the listener allows; its result is published to that topic,
//     with the Correlation Data, when the session subscribes to it. PUBACK
//     carries a reason code and the gateway's failure code as its reason
//     string, so a refusal no longer closes the connection.
//
// The author is named in CONNECT's user properties (airc-nick, and
// airc-token for a registered account), checked with AIRC at once, and is
// the session's owner: a session is resumed only by the same author. The
// CONNECT password stays the listener access password.

const (
	// mqtt5MaxSessionExpiry caps a session's life after its connection ends.
	mqtt5MaxSessionExpiry = time.Hour
	// mqtt5MaxSessions caps the sessions the listener remembers.
	mqtt5MaxSessions = 1024
	// mqtt5MaxInflight caps unacknowledged QoS 1 messages to one client,
	// and mqtt5MaxInflightBytes their size, so the sessions the listener
	// keeps hold at most 256 MiB.
	mqtt5MaxInflight      = 64
	mqtt5MaxInflightBytes = 256 << 10
	// mqtt5ReceiveMaximum is how many QoS 1 publications the gateway takes
	// from a client at once (it handles them in order).
	mqtt5ReceiveMaximum = 16
	// mqtt5MaxFollowTargets is how many rooms, threads and the inbox one
	// session subscribes to: one check.
	mqtt5MaxFollowTargets = core.MaxCheckRooms
	// mqtt5MaxReplyTopics caps a session's response topic subscriptions.
	mqtt5MaxReplyTopics = 16
	mqtt5RequestTimeout = 4 * time.Second
	mqtt5FollowLimit    = 200
)

// MQTT 5 reason codes used here.
const (
	mqttSuccess              = 0x00
	mqttNoSubscription       = 0x11
	mqttUnspecified          = 0x80
	mqttMalformed            = 0x81
	mqttProtocolError        = 0x82
	mqttImplementation       = 0x83
	mqttBadCredentials       = 0x86
	mqttNotAuthorized        = 0x87
	mqttServerUnavailable    = 0x88
	mqttBadAuthMethod        = 0x8C
	mqttKeepAliveTimeout     = 0x8D
	mqttSessionTakenOver     = 0x8E
	mqttTopicFilterInvalid   = 0x8F
	mqttTopicNameInvalid     = 0x90
	mqttTopicAliasInvalid    = 0x94
	mqttQuotaExceeded        = 0x97
	mqttPayloadFormatInvalid = 0x99
	mqttRetainNotSupported   = 0x9A
	mqttQoSNotSupported      = 0x9B
	mqttSharedNotSupported   = 0x9E
	mqttWildcardNotSupported = 0xA2
)

// mqtt5Store is the listener's sessions, by client identifier.
type mqtt5Store struct {
	mu       sync.Mutex
	sessions map[string]*mqtt5State
}

// mqtt5State is one session: who owns it, what it subscribes to and where
// each subscription has got to, and its unacknowledged QoS 1 messages.
type mqtt5State struct {
	owner     string // the author scope; "" for a session without an author
	expiry    time.Duration
	expiresAt time.Time // zero while a connection holds the session
	subs      map[string]*mqtt5Sub
	inflight  []*mqtt5Outbound // in the order they were sent
	// inflightBytes is the size of inflight's packets.
	inflightBytes int
	nextID        uint16
}

type mqtt5Kind uint8

const (
	mqtt5Room mqtt5Kind = iota + 1
	mqtt5Thread
	mqtt5Inbox
	mqtt5Reply // a response topic: carries only replies to this session
)

type mqtt5Sub struct {
	filter string
	kind   mqtt5Kind
	target string // the room name or the thread's root ID
	qos    byte
	// after is the position delivered so far: the next message comes after
	// it.
	after string
}

type mqtt5Outbound struct {
	id    uint16
	topic string
	props mqttwire.Properties
	body  []byte
}

func newMQTT5Store() *mqtt5Store { return &mqtt5Store{sessions: map[string]*mqtt5State{}} }

// take hands the session for clientID to a new connection: the existing
// one when resume asks for it and owner owns it, else a fresh one. present
// reports a resumed session. A session without an author is never
// resumed: a client ID alone proves nothing, and its pending replies
// answer someone's reads.
func (s *mqtt5Store) take(clientID, owner string, resume bool, expiry time.Duration) (state *mqtt5State, present, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if old := s.sessions[clientID]; old != nil {
		if resume && owner != "" && old.owner == owner && (old.expiresAt.IsZero() || now.Before(old.expiresAt)) {
			old.expiresAt, old.expiry = time.Time{}, expiry
			return old, true, true
		}
		delete(s.sessions, clientID)
	}
	if len(s.sessions) >= mqtt5MaxSessions {
		s.sweepLocked(now)
		if len(s.sessions) >= mqtt5MaxSessions {
			return nil, false, false
		}
	}
	state = &mqtt5State{owner: owner, expiry: expiry, subs: map[string]*mqtt5Sub{}}
	s.sessions[clientID] = state
	return state, false, true
}

// release ends a connection's hold on its session: it is forgotten now
// without a Session Expiry Interval, or once that has passed.
func (s *mqtt5Store) release(clientID string, state *mqtt5State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[clientID] != state {
		return
	}
	if state.expiry <= 0 || state.owner == "" {
		delete(s.sessions, clientID)
		return
	}
	state.expiresAt = time.Now().Add(state.expiry)
}

func (s *mqtt5Store) sweepLocked(now time.Time) {
	for id, state := range s.sessions {
		if !state.expiresAt.IsZero() && !now.Before(state.expiresAt) {
			delete(s.sessions, id)
		}
	}
}

// sweep forgets expired sessions every minute until ctx ends.
func (s *mqtt5Store) sweep(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.mu.Lock()
			s.sweepLocked(now)
			s.mu.Unlock()
		}
	}
}

// mqtt5Conn is one MQTT 5 connection and the session it holds.
type mqtt5Conn struct {
	ctx      context.Context
	conn     net.Conn
	d        *dispatch.Dispatcher
	requests chan struct{}
	routes   map[string]config.MQTTRoute
	in       dispatch.Ingress
	state    *mqtt5State
	// stateMu guards state, which the read loop and the follow share.
	stateMu sync.Mutex
	// writeMu orders writes from the read loop, the follow and a takeover.
	writeMu sync.Mutex
	// slots holds one token per unacknowledged QoS 1 message, so no more
	// than the client's Receive Maximum are outstanding.
	slots chan struct{}
	// acked is closed, and replaced, when an acknowledgement frees room
	// under mqtt5MaxInflightBytes.
	acked chan struct{}
	// maxPacket is the client's Maximum Packet Size.
	maxPacket int
	// problems is false when the client asked for no reason strings.
	problems bool
	follow   struct {
		cancel context.CancelFunc
		done   chan struct{}
	}
	identified bool
}

// serveMQTT5 runs an MQTT 5 connection after its CONNECT. It owns the
// connection until it returns; the caller closes it.
func serveMQTT5(ctx context.Context, conn net.Conn, connect mqttwire.Packet, d *dispatch.Dispatcher, requests chan struct{}, routes map[string]config.MQTTRoute, owners *mqttSessions, sessions *mqtt5Store, session *mqttSession, author *dispatch.Session, clientID *string) {
	c := &mqtt5Conn{ctx: ctx, conn: conn, d: d, requests: requests, routes: routes, problems: true, maxPacket: mqttwire.MaxBodyBytesV5 + 8}
	refuse := func(reason byte, why string) {
		_ = conn.SetWriteDeadline(time.Now().Add(mqttDeadline))
		_ = mqttwire.WriteConnackV5(conn, false, reason, c.reasonProps(why))
	}
	p := connect.Props
	if p.RequestProblemInfo != nil && *p.RequestProblemInfo == 0 {
		c.problems = false
	}
	switch {
	case connect.Will:
		refuse(mqttImplementation, "wills are not supported")
		return
	case p.AuthMethod != "":
		refuse(mqttBadAuthMethod, "enhanced authentication is not supported")
		return
	}
	if p.MaximumPacketSize != nil {
		c.maxPacket = int(*p.MaximumPacketSize)
	}
	receive := mqtt5MaxInflight
	if p.ReceiveMaximum != nil {
		receive = min(receive, int(*p.ReceiveMaximum))
	}
	c.slots = make(chan struct{}, receive)
	c.acked = make(chan struct{})
	var password core.Password
	var err error
	if connect.HasPassword && connect.Password != "" {
		if password, err = core.NewPassword(connect.Password); err != nil {
			refuse(mqttBadCredentials, string(core.Forbidden))
			return
		}
	}
	if err := d.Connect(author, password); err != nil {
		refuse(mqttBadCredentials, string(core.Forbidden))
		return
	}
	c.in = dispatch.Ingress{Source: d.Source(conn.RemoteAddr()), Session: author}
	owner := ""
	if nick, ok := p.UserProperty("airc-nick"); ok {
		a := core.Author{Nick: nick}
		var credential core.Credential
		if token, ok := p.UserProperty("airc-token"); ok {
			if credential, err = core.NewCredential(token); err != nil {
				refuse(mqttBadCredentials, string(core.AuthenticationFailed))
				return
			}
			a.Registered = true
		}
		if !a.Valid() {
			refuse(mqttBadCredentials, string(core.InvalidRequest))
			return
		}
		identify := core.Submission{Request: core.Request{Version: 1, RequestID: "mqtt-identify", Operation: core.AccountIdentify}, Author: &a, Credential: credential}
		checkCtx, cancel := context.WithTimeout(ctx, mqtt5RequestTimeout)
		_, err := d.SubmitDecoded(checkCtx, c.in, identify, time.Now())
		cancel()
		if err != nil {
			reason, code := mqtt5Reason(err, true)
			refuse(reason, code)
			return
		}
		owner, c.identified = a.Scope(), true
	}
	assigned := ""
	*clientID = connect.ClientID
	if *clientID == "" {
		*clientID = "airc-" + rand.Text()
		assigned = *clientID
	}
	expiry := time.Duration(0)
	if p.SessionExpiry != nil {
		expiry = min(time.Duration(*p.SessionExpiry)*time.Second, mqtt5MaxSessionExpiry)
	}
	session.takeover = func() { c.disconnect(mqttSessionTakenOver, "") }
	if !owners.claim(ctx, *clientID, session) {
		return
	}
	state, present, ok := sessions.take(*clientID, owner, !connect.CleanSession, expiry)
	if !ok {
		refuse(mqttQuotaExceeded, "too many sessions")
		return
	}
	c.state = state
	defer sessions.release(*clientID, state)
	one, zero := byte(1), byte(0)
	receiveMax := uint16(mqtt5ReceiveMaximum)
	maxPacket := uint32(mqttwire.MaxBodyBytesV5 + 8)
	ack := mqttwire.Properties{AssignedClientID: assigned, MaximumQoS: &one, RetainAvailable: &zero, WildcardAvailable: &zero, SubIDAvailable: &zero, SharedAvailable: &zero, ReceiveMaximum: &receiveMax, MaximumPacketSize: &maxPacket}
	if p.SessionExpiry != nil && time.Duration(*p.SessionExpiry)*time.Second > expiry {
		granted := uint32(expiry / time.Second)
		ack.SessionExpiry = &granted
	}
	idle := time.Duration(connect.KeepAlive) * time.Second * 3 / 2
	if connect.KeepAlive == 0 {
		keep := uint16(mqttIdle / time.Second / 2)
		ack.ServerKeepAlive = &keep
		idle = mqttIdle
	}
	c.writeMu.Lock()
	err = conn.SetWriteDeadline(time.Now().Add(mqttDeadline))
	if err == nil {
		err = mqttwire.WriteConnackV5(conn, present, mqttSuccess, ack)
	}
	c.writeMu.Unlock()
	if err != nil {
		return
	}
	if present {
		c.resend()
	}
	c.restartFollow()
	defer c.stopFollow()
	c.readLoop(idle, expiry > 0)
}

// reasonProps carries a reason string when the client accepts them.
func (c *mqtt5Conn) reasonProps(why string) mqttwire.Properties {
	if !c.problems || why == "" {
		return mqttwire.Properties{}
	}
	return mqttwire.Properties{ReasonString: why}
}

// write sends one encoded packet within the write deadline.
func (c *mqtt5Conn) write(packet []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(mqttDeadline)); err != nil {
		return err
	}
	return mqttwire.WritePacket(c.conn, packet)
}

// disconnect tells the client why the connection ends, best effort.
func (c *mqtt5Conn) disconnect(reason byte, why string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = mqttwire.WriteDisconnectV5(c.conn, reason, c.reasonProps(why))
}

func (c *mqtt5Conn) readLoop(idle time.Duration, expiring bool) {
	last := time.Now()
	for {
		if c.ctx.Err() != nil || c.conn.SetReadDeadline(last.Add(idle)) != nil {
			return
		}
		packet, err := mqttwire.ReadV5(c.conn)
		switch {
		case errors.Is(err, mqttwire.ErrProtocol):
			c.disconnect(mqttProtocolError, "")
			return
		case errors.Is(err, mqttwire.ErrPacket):
			c.disconnect(mqttMalformed, "")
			return
		case err != nil:
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() && c.ctx.Err() == nil {
				c.disconnect(mqttKeepAliveTimeout, "")
			}
			return
		}
		last = time.Now()
		switch packet.Kind {
		case mqttwire.Ping:
			if c.write([]byte{0xd0, 0}) != nil {
				return
			}
		case mqttwire.Disconnect:
			if e := packet.Props.SessionExpiry; e != nil {
				if !expiring && *e > 0 {
					c.disconnect(mqttProtocolError, "a session without expiry cannot gain one at DISCONNECT")
					return
				}
				c.stateMu.Lock()
				c.state.expiry = min(time.Duration(*e)*time.Second, mqtt5MaxSessionExpiry)
				c.stateMu.Unlock()
			}
			return
		case mqttwire.PubAck:
			c.acknowledged(packet.ID)
		case mqttwire.Publish:
			if !c.publish(packet) {
				return
			}
		case mqttwire.Subscribe:
			if !c.subscribe(packet) {
				return
			}
		case mqttwire.Unsubscribe:
			if !c.unsubscribe(packet) {
				return
			}
		default: // AUTH without a negotiated method
			c.disconnect(mqttProtocolError, "")
			return
		}
	}
}

// acknowledged takes a client's PUBACK: the message is delivered.
func (c *mqtt5Conn) acknowledged(id uint16) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	for i, out := range c.state.inflight {
		if out.id == id {
			c.state.inflight = append(c.state.inflight[:i], c.state.inflight[i+1:]...)
			c.state.inflightBytes -= out.size()
			select {
			case <-c.slots:
			default:
			}
			close(c.acked)
			c.acked = make(chan struct{})
			return
		}
	}
}

// resend sends a resumed session's unacknowledged messages again, with
// their original packet identifiers, as MQTT requires.
func (c *mqtt5Conn) resend() {
	c.stateMu.Lock()
	inflight := append([]*mqtt5Outbound(nil), c.state.inflight...)
	c.stateMu.Unlock()
	for _, out := range inflight {
		select {
		case c.slots <- struct{}{}:
		default:
		}
		if c.write(mqttwire.PublishV5(out.topic, out.id, 1, true, out.props, out.body)) != nil {
			return
		}
	}
}

// send publishes body to topic at qos. A QoS 1 message waits for a free
// slot under the client's Receive Maximum, and for room under
// mqtt5MaxInflightBytes, and is kept until acknowledged. With wait false
// it does not wait: it is sent at QoS 0 instead, which a subscription's
// QoS allows, so the read loop never blocks on the acknowledgements only
// it can read. A message larger than the client's Maximum Packet Size is
// dropped, as MQTT 5 directs.
func (c *mqtt5Conn) send(ctx context.Context, topic string, qos byte, props mqttwire.Properties, body []byte, wait bool) error {
	if qos == 1 {
		if wait {
			select {
			case c.slots <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			select {
			case c.slots <- struct{}{}:
			default:
				qos = 0
			}
		}
	}
	if qos == 0 {
		packet := mqttwire.PublishV5(topic, 0, 0, false, props, body)
		if len(packet) > c.maxPacket {
			return nil
		}
		return c.write(packet)
	}
	for {
		c.stateMu.Lock()
		out := &mqtt5Outbound{topic: topic, props: props, body: body}
		size := out.size()
		if size > c.maxPacket {
			c.stateMu.Unlock()
			<-c.slots
			return nil
		}
		if c.state.inflightBytes+size <= mqtt5MaxInflightBytes || len(c.state.inflight) == 0 {
			out.id = c.nextIDLocked()
			c.state.inflight = append(c.state.inflight, out)
			c.state.inflightBytes += size
			c.stateMu.Unlock()
			return c.write(mqttwire.PublishV5(topic, out.id, 1, false, props, body))
		}
		acked := c.acked
		c.stateMu.Unlock()
		if !wait {
			<-c.slots
			packet := mqttwire.PublishV5(topic, 0, 0, false, props, body)
			if len(packet) > c.maxPacket {
				return nil
			}
			return c.write(packet)
		}
		select {
		case <-acked:
		case <-ctx.Done():
			<-c.slots
			return ctx.Err()
		}
	}
}

// size is the message's packet size, near enough: its topic, body and
// properties.
func (o *mqtt5Outbound) size() int {
	return len(mqttwire.PublishV5(o.topic, 1, 1, false, o.props, o.body))
}

// nextIDLocked is a packet identifier no unacknowledged message uses.
func (c *mqtt5Conn) nextIDLocked() uint16 {
	for {
		c.state.nextID++
		if c.state.nextID == 0 {
			c.state.nextID = 1
		}
		used := false
		for _, out := range c.state.inflight {
			if out.id == c.state.nextID {
				used = true
				break
			}
		}
		if !used {
			return c.state.nextID
		}
	}
}

// publish handles a client PUBLISH: a request on a route topic. It reports
// false when the connection must end.
func (c *mqtt5Conn) publish(packet mqttwire.Packet) bool {
	switch {
	case packet.QoS == 2:
		c.disconnect(mqttQoSNotSupported, "")
		return false
	case packet.Retain:
		c.disconnect(mqttRetainNotSupported, "")
		return false
	case packet.Props.TopicAlias != nil || packet.Topic == "":
		c.disconnect(mqttTopicAliasInvalid, "")
		return false
	}
	reason, why := c.request(packet)
	if packet.QoS == 1 {
		c.writeMu.Lock()
		err := c.conn.SetWriteDeadline(time.Now().Add(mqttDeadline))
		if err == nil {
			err = mqttwire.WritePubackV5(c.conn, packet.ID, reason, c.reasonProps(why))
		}
		c.writeMu.Unlock()
		return err == nil
	}
	return true
}

// request runs a publication's contract request and, with a Response
// Topic, publishes its result there. It answers the PUBACK reason code and
// reason string.
func (c *mqtt5Conn) request(packet mqttwire.Packet) (byte, string) {
	if _, ok := c.routes[packet.Topic]; !ok {
		return mqttTopicNameInvalid, ""
	}
	if f := packet.Props.PayloadFormat; f != nil && *f == 1 && !utf8.ValidString(packet.Payload) {
		return mqttPayloadFormatInvalid, ""
	}
	responseTopic := packet.Props.ResponseTopic
	if responseTopic != "" && !mqttwire.ValidTopic(responseTopic) {
		return mqttTopicNameInvalid, "invalid response topic"
	}
	sub, err := core.DecodeSubmission([]byte(packet.Payload))
	if err != nil {
		return mqttPayloadFormatInvalid, string(core.InvalidRequest)
	}
	op := sub.Request.Operation
	if responseTopic == "" && !op.IsMutation() && op != core.AccountIdentify {
		return mqttImplementation, "a read needs a response topic"
	}
	select {
	case c.requests <- struct{}{}:
	default:
		return mqttQuotaExceeded, string(core.CapacityExceeded)
	}
	ctx, cancel := context.WithTimeout(c.ctx, mqtt5RequestTimeout)
	result, err := c.d.SubmitDecoded(ctx, c.in, sub, time.Now())
	cancel()
	<-c.requests
	if err == nil && op == core.AccountIdentify && sub.Author != nil {
		// An anonymous session becomes the identified author's.
		c.identified = true
		c.stateMu.Lock()
		if c.state.owner == "" {
			c.state.owner = sub.Author.Scope()
		}
		c.stateMu.Unlock()
	}
	if responseTopic != "" {
		c.reply(responseTopic, packet.Props.CorrelationData, result, err)
	}
	if err != nil {
		return mqtt5Reason(err, false)
	}
	if op.IsMutation() && (result.Result == nil || !result.Result.Durable) {
		return mqttUnspecified, string(core.UpstreamUnavailable)
	}
	return mqttSuccess, ""
}

// reply publishes a request's result to its response topic when this
// session subscribes to it; with no subscriber it goes nowhere, as on any
// broker.
func (c *mqtt5Conn) reply(topic string, correlation []byte, result dispatch.DetachedResult, err error) {
	c.stateMu.Lock()
	sub := c.state.subs[topic]
	c.stateMu.Unlock()
	if sub == nil || sub.kind != mqtt5Reply {
		return
	}
	var body []byte
	if err != nil {
		_, failure := errorFailure(err)
		body, _ = json.Marshal(struct {
			Version int          `json:"version"`
			Error   core.Failure `json:"error"`
		}{1, failure})
	} else {
		body, _ = json.Marshal(result)
	}
	format := byte(1)
	props := mqttwire.Properties{PayloadFormat: &format, ContentType: "application/json", CorrelationData: correlation}
	_ = c.send(c.ctx, topic, sub.qos, props, body, false)
}

// mqtt5Reason maps a gateway refusal to an MQTT 5 reason code and the
// failure code as its reason string. At CONNECT, a bad credential is 0x86.
func mqtt5Reason(err error, connecting bool) (byte, string) {
	_, failure := errorFailure(err)
	code := string(failure.Code)
	switch failure.Code {
	case core.AuthenticationFailed:
		if connecting {
			return mqttBadCredentials, code
		}
		return mqttNotAuthorized, code
	case core.Forbidden, core.NickRegistered, core.NickInUse:
		return mqttNotAuthorized, code
	case core.CapacityExceeded:
		return mqttQuotaExceeded, code
	case core.UpstreamUnavailable:
		if connecting {
			return mqttServerUnavailable, code
		}
	}
	if failure.Transient {
		return mqttUnspecified, code
	}
	return mqttImplementation, code
}

// mqtt5Topic is the topic a subscription's messages are published on.
func mqtt5Topic(kind mqtt5Kind, target string) string {
	switch kind {
	case mqtt5Room:
		return "airc/room/" + strings.TrimPrefix(target, "#")
	case mqtt5Thread:
		return "airc/thread/" + target
	case mqtt5Inbox:
		return "airc/inbox"
	}
	return target
}

// classify reads a topic filter: a room, a thread, the inbox, or a
// response topic. It answers a SUBACK refusal otherwise.
func (c *mqtt5Conn) classify(filter string) (*mqtt5Sub, byte) {
	switch {
	case strings.HasPrefix(filter, "$share/"):
		return nil, mqttSharedNotSupported
	case strings.ContainsAny(filter, "+#"):
		return nil, mqttWildcardNotSupported
	case strings.HasPrefix(filter, "$") || containsControl(filter):
		return nil, mqttTopicFilterInvalid
	}
	if _, ok := c.routes[filter]; ok {
		return nil, mqttTopicFilterInvalid // a request topic delivers nothing
	}
	if name, ok := strings.CutPrefix(filter, "airc/room/"); ok {
		if !strings.HasPrefix(name, "&") {
			name = "#" + name
		}
		target := core.Target{Kind: "room", Name: name}
		if target.Normalize() != nil {
			return nil, mqttTopicFilterInvalid
		}
		return &mqtt5Sub{filter: filter, kind: mqtt5Room, target: target.Name}, mqttSuccess
	}
	if id, ok := strings.CutPrefix(filter, "airc/thread/"); ok {
		if !protocol.ValidMessageID(id) {
			return nil, mqttTopicFilterInvalid
		}
		return &mqtt5Sub{filter: filter, kind: mqtt5Thread, target: id}, mqttSuccess
	}
	if filter == "airc/inbox" {
		return &mqtt5Sub{filter: filter, kind: mqtt5Inbox}, mqttSuccess
	}
	if strings.HasPrefix(filter, "airc/") || !mqttwire.ValidTopic(filter) {
		return nil, mqttTopicFilterInvalid
	}
	return &mqtt5Sub{filter: filter, kind: mqtt5Reply, target: filter}, mqttSuccess
}

func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// subscribe handles a SUBSCRIBE: each room, thread or inbox is checked
// with AIRC as the session's author, which also gives its starting
// position (after the latest message, or the airc-after user property: a
// message ID, or "retained" for the oldest retained message).
func (c *mqtt5Conn) subscribe(packet mqttwire.Packet) bool {
	after, _ := packet.Props.UserProperty("airc-after")
	if after != "" && after != "retained" && !protocol.ValidMessageID(after) {
		after = "invalid"
	}
	reasons := make([]byte, len(packet.Subscriptions))
	changed := false
	for i, s := range packet.Subscriptions {
		sub, reason := c.classify(s.Filter)
		if sub == nil {
			reasons[i] = reason
			continue
		}
		sub.qos = min(s.QoS, 1)
		c.stateMu.Lock()
		existing := c.state.subs[s.Filter]
		follows, replies := c.countLocked()
		c.stateMu.Unlock()
		if existing != nil {
			// A repeated subscription keeps its position; only its QoS
			// changes.
			c.stateMu.Lock()
			existing.qos = sub.qos
			c.stateMu.Unlock()
			reasons[i] = sub.qos
			continue
		}
		if sub.kind == mqtt5Reply && replies >= mqtt5MaxReplyTopics || sub.kind != mqtt5Reply && follows >= mqtt5MaxFollowTargets {
			reasons[i] = mqttQuotaExceeded
			continue
		}
		if sub.kind != mqtt5Reply {
			if !c.identified {
				reasons[i] = mqttNotAuthorized
				continue
			}
			if after == "invalid" {
				reasons[i] = mqttImplementation
				continue
			}
			position, err := c.position(sub, after)
			if err != nil {
				reasons[i], _ = mqtt5Reason(err, false)
				continue
			}
			sub.after = position
			changed = true
		}
		c.stateMu.Lock()
		c.state.subs[s.Filter] = sub
		c.stateMu.Unlock()
		reasons[i] = sub.qos
	}
	c.writeMu.Lock()
	err := c.conn.SetWriteDeadline(time.Now().Add(mqttDeadline))
	if err == nil {
		err = mqttwire.WriteSubackV5(c.conn, packet.ID, reasons, mqttwire.Properties{})
	}
	c.writeMu.Unlock()
	if changed {
		c.restartFollow()
	}
	return err == nil
}

func (c *mqtt5Conn) countLocked() (follows, replies int) {
	for _, sub := range c.state.subs {
		if sub.kind == mqtt5Reply {
			replies++
		} else {
			follows++
		}
	}
	return follows, replies
}

// position checks one subscription with AIRC as the session's author and
// answers where it starts: after the latest message, or after start.
func (c *mqtt5Conn) position(sub *mqtt5Sub, start string) (string, error) {
	r := core.Request{Version: 1, RequestID: "mqtt-subscribe", Operation: core.Check, Limit: 1}
	from := "recent"
	if start != "" {
		from = start
	}
	switch sub.kind {
	case mqtt5Room:
		r.Rooms = []core.CheckRoom{{Name: sub.target, After: from}}
	case mqtt5Thread:
		r.Threads = []core.CheckThread{{ID: sub.target, After: from}}
	case mqtt5Inbox:
		r.Inbox = &core.CheckInbox{After: from}
	}
	ctx, cancel := context.WithTimeout(c.ctx, mqtt5RequestTimeout)
	defer cancel()
	result, err := c.d.SubmitDecoded(ctx, c.in, core.Submission{Request: r}, time.Now())
	if err != nil {
		return "", err
	}
	if start != "" {
		return start, nil // read from there
	}
	var page *core.CheckPage
	switch {
	case result.Check == nil:
	case sub.kind == mqtt5Room && len(result.Check.Rooms) == 1:
		page = &result.Check.Rooms[0]
	case sub.kind == mqtt5Thread && len(result.Check.Threads) == 1:
		page = &result.Check.Threads[0]
	case sub.kind == mqtt5Inbox:
		page = result.Check.Inbox
	}
	if page == nil {
		return "", errors.New("check answered without the subscription's page")
	}
	if n := len(page.Messages); n > 0 && page.Cursor == "" {
		return page.Messages[n-1].ID, nil
	}
	return page.Cursor, nil
}

func (c *mqtt5Conn) unsubscribe(packet mqttwire.Packet) bool {
	reasons := make([]byte, len(packet.Filters))
	changed := false
	c.stateMu.Lock()
	for i, filter := range packet.Filters {
		sub := c.state.subs[filter]
		if sub == nil {
			reasons[i] = mqttNoSubscription
			continue
		}
		delete(c.state.subs, filter)
		changed = changed || sub.kind != mqtt5Reply
	}
	c.stateMu.Unlock()
	c.writeMu.Lock()
	err := c.conn.SetWriteDeadline(time.Now().Add(mqttDeadline))
	if err == nil {
		err = mqttwire.WriteUnsubackV5(c.conn, packet.ID, reasons)
	}
	c.writeMu.Unlock()
	if changed {
		c.restartFollow()
	}
	return err == nil
}

func (c *mqtt5Conn) stopFollow() {
	if c.follow.cancel != nil {
		c.follow.cancel()
		<-c.follow.done
		c.follow.cancel, c.follow.done = nil, nil
	}
}

// restartFollow replaces the connection's follow with one over its current
// subscriptions, from their positions. Only the read loop calls it.
func (c *mqtt5Conn) restartFollow() {
	c.stopFollow()
	c.stateMu.Lock()
	var subs []*mqtt5Sub
	for _, sub := range c.state.subs {
		if sub.kind != mqtt5Reply {
			subs = append(subs, sub)
		}
	}
	c.stateMu.Unlock()
	if len(subs) == 0 || !c.identified {
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	c.follow.cancel, c.follow.done = cancel, done
	go func() {
		defer close(done)
		c.runFollow(ctx, subs)
	}()
}

// runFollow keeps one followed check over subs running until ctx ends,
// resuming when its lease ends and backing off when it fails.
func (c *mqtt5Conn) runFollow(ctx context.Context, subs []*mqtt5Sub) {
	backoff := time.Second
	for ctx.Err() == nil {
		r := core.Request{Version: 1, RequestID: "mqtt-follow", Operation: core.Check, Limit: mqtt5FollowLimit}
		var order []*mqtt5Sub
		var inbox *mqtt5Sub
		c.stateMu.Lock()
		for _, sub := range subs {
			after := sub.after
			if after == "" {
				after = "recent"
			}
			switch sub.kind {
			case mqtt5Room:
				r.Rooms = append(r.Rooms, core.CheckRoom{Name: sub.target, After: after})
				order = append(order, sub)
			case mqtt5Inbox:
				inbox = sub
				r.Inbox = &core.CheckInbox{After: after}
			}
		}
		for _, sub := range subs {
			if sub.kind == mqtt5Thread {
				after := sub.after
				if after == "" {
					after = "recent"
				}
				r.Threads = append(r.Threads, core.CheckThread{ID: sub.target, After: after})
				order = append(order, sub)
			}
		}
		c.stateMu.Unlock()
		if inbox != nil {
			order = append(order, inbox)
		}
		err := c.d.SubmitDecodedStream(ctx, c.in, core.Submission{Request: r}, time.Now(), dispatch.StreamSink{Check: func(result core.CheckResult) error {
			return c.deliver(ctx, order, result)
		}})
		if ctx.Err() != nil {
			return
		}
		if err == nil || errors.Is(err, backend.ErrStreamRenew) {
			backoff = time.Second
			continue // the lease ended: resume from the positions reached
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// deliver publishes one follow frame's messages, page by page in the
// request's order, and moves each subscription's position past them.
func (c *mqtt5Conn) deliver(ctx context.Context, order []*mqtt5Sub, result core.CheckResult) error {
	pages := append(append([]core.CheckPage(nil), result.Rooms...), result.Threads...)
	if result.Inbox != nil {
		pages = append(pages, *result.Inbox)
	}
	if len(pages) != len(order) {
		return errors.New("follow frame without a page per subscription")
	}
	format := byte(1)
	for i, page := range pages {
		sub := order[i]
		if page.Status == "withheld" || page.Status == "expired" {
			continue
		}
		c.stateMu.Lock()
		qos := sub.qos
		c.stateMu.Unlock()
		for j, m := range page.Messages {
			body, err := json.Marshal(m)
			if err != nil {
				return err
			}
			props := mqttwire.Properties{PayloadFormat: &format, ContentType: "application/json"}
			if j == 0 && page.Gap {
				props.User = [][2]string{{"airc-gap", "true"}}
			}
			if err := c.send(ctx, mqtt5Topic(sub.kind, sub.target), qos, props, body, true); err != nil {
				return err
			}
			c.stateMu.Lock()
			sub.after = m.ID
			c.stateMu.Unlock()
		}
		if page.Cursor != "" {
			c.stateMu.Lock()
			sub.after = page.Cursor
			c.stateMu.Unlock()
		}
	}
	return nil
}
