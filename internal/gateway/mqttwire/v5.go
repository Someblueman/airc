package mqttwire

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf8"
)

// The MQTT 5 profile: QoS 0 and 1 in both directions, request/response
// (Response Topic, Correlation Data), subscriptions with no wildcards, and
// sessions that outlive a connection (Clean Start, Session Expiry Interval).
// Retained messages, wills, topic aliases, shared subscriptions,
// subscription identifiers and enhanced authentication are refused, and
// CONNACK says so. As for 3.1.1, parsing authenticates and admits nothing.

const (
	PubAck      Kind = 4
	Subscribe   Kind = 8
	Unsubscribe Kind = 10
	AuthPacket  Kind = 15
)

// MaxBodyBytesV5 bounds an MQTT 5 packet body: a publication and its
// properties.
const MaxBodyBytesV5 = MaxBodyBytes + maxPropertyBytes

const (
	maxPropertyBytes  = 4096
	maxPropertyString = 1024
	maxUserProperties = 32
	maxFilters        = 32
	// MaxFilterBytes bounds one SUBSCRIBE or UNSUBSCRIBE topic filter.
	MaxFilterBytes = 256
)

// ErrProtocol is a well-formed packet that breaks an MQTT 5 rule (a
// repeated property, a property the packet may not carry); the connection
// ends with reason 0x82. ErrPacket (0x81) is a malformed one.
var ErrProtocol = errors.New("MQTT 5 protocol error")

// Properties are the MQTT 5 properties this profile reads or writes. A
// pointer or empty value is an absent property.
type Properties struct {
	PayloadFormat       *byte   // 0x01
	MessageExpiry       *uint32 // 0x02
	ContentType         string  // 0x03
	ResponseTopic       string  // 0x08
	CorrelationData     []byte  // 0x09
	SubscriptionIDs     []int   // 0x0B
	SessionExpiry       *uint32 // 0x11
	AssignedClientID    string  // 0x12
	ServerKeepAlive     *uint16 // 0x13
	AuthMethod          string  // 0x15
	AuthData            []byte  // 0x16
	RequestProblemInfo  *byte   // 0x17
	WillDelay           *uint32 // 0x18
	RequestResponseInfo *byte   // 0x19
	ResponseInfo        string  // 0x1A
	ServerReference     string  // 0x1C
	ReasonString        string  // 0x1F
	ReceiveMaximum      *uint16 // 0x21
	TopicAliasMaximum   *uint16 // 0x22
	TopicAlias          *uint16 // 0x23
	MaximumQoS          *byte   // 0x24
	RetainAvailable     *byte   // 0x25
	User                [][2]string
	MaximumPacketSize   *uint32 // 0x27
	WildcardAvailable   *byte   // 0x28
	SubIDAvailable      *byte   // 0x29
	SharedAvailable     *byte   // 0x2A
}

// UserProperty returns the first user property named key.
func (p Properties) UserProperty(key string) (string, bool) {
	for _, pair := range p.User {
		if pair[0] == key {
			return pair[1], true
		}
	}
	return "", false
}

// Subscription is one SUBSCRIBE entry: a topic filter and its options.
type Subscription struct {
	Filter string
	QoS    byte
	// NoLocal, RetainAsPublished and RetainHandling are the other options;
	// this profile has no retained messages or self-publication, so they
	// change nothing.
	NoLocal, RetainAsPublished bool
	RetainHandling             byte
}

// Which properties each client packet may carry.
var (
	connectProperties     = propertySet(0x11, 0x21, 0x27, 0x22, 0x19, 0x17, 0x26, 0x15, 0x16)
	willProperties        = propertySet(0x18, 0x01, 0x02, 0x03, 0x08, 0x09, 0x26)
	publishProperties     = propertySet(0x01, 0x02, 0x23, 0x08, 0x09, 0x26, 0x03)
	pubackProperties      = propertySet(0x1F, 0x26)
	subscribeProperties   = propertySet(0x0B, 0x26)
	unsubscribeProperties = propertySet(0x26)
	disconnectProperties  = propertySet(0x11, 0x1F, 0x26, 0x1C)
)

func propertySet(ids ...byte) map[byte]bool {
	set := make(map[byte]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func (f *fields) byte1() byte {
	if f.err != nil || len(f.body)-f.position < 1 {
		f.err = ErrPacket
		return 0
	}
	value := f.body[f.position]
	f.position++
	return value
}

func (f *fields) uint32() uint32 {
	if f.err != nil || len(f.body)-f.position < 4 {
		f.err = ErrPacket
		return 0
	}
	value := binary.BigEndian.Uint32(f.body[f.position:])
	f.position += 4
	return value
}

// varint is MQTT's variable byte integer, at most four bytes.
func (f *fields) varint() int {
	value := 0
	for index := 0; index < 4; index++ {
		b := f.byte1()
		if f.err != nil {
			return 0
		}
		value |= int(b&127) << (7 * index)
		if b&128 == 0 {
			return value
		}
	}
	f.err = ErrPacket
	return 0
}

func (f *fields) binary(limit int) []byte {
	size := int(f.uint16())
	if f.err != nil || size > limit || size > len(f.body)-f.position {
		f.err = ErrPacket
		return nil
	}
	value := append([]byte(nil), f.body[f.position:f.position+size]...)
	f.position += size
	return value
}

// utf8text is text that must be valid UTF-8 without NUL, as every MQTT 5
// string must be.
func (f *fields) utf8text(limit int) string {
	value := f.text(limit)
	if f.err == nil && (!utf8.ValidString(value) || containsNUL(value)) {
		f.err = ErrPacket
	}
	return value
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// properties reads a property list, refusing a property the packet may not
// carry and a repeated one (user properties and subscription identifiers
// excepted).
func (f *fields) properties(allowed map[byte]bool) (Properties, error) {
	var p Properties
	size := f.varint()
	if f.err != nil || size > maxPropertyBytes || size > len(f.body)-f.position {
		return p, ErrPacket
	}
	end := f.position + size
	seen := map[byte]bool{}
	for f.position < end && f.err == nil {
		id := byte(f.varint())
		if f.err != nil {
			break
		}
		if !allowed[id] {
			return p, ErrProtocol
		}
		if seen[id] && id != 0x26 && id != 0x0B {
			return p, ErrProtocol
		}
		seen[id] = true
		switch id {
		case 0x01, 0x17, 0x19:
			v := f.byte1()
			if v > 1 {
				return p, ErrProtocol
			}
			switch id {
			case 0x01:
				p.PayloadFormat = &v
			case 0x17:
				p.RequestProblemInfo = &v
			case 0x19:
				p.RequestResponseInfo = &v
			}
		case 0x02, 0x11, 0x18, 0x27:
			v := f.uint32()
			switch id {
			case 0x02:
				p.MessageExpiry = &v
			case 0x11:
				p.SessionExpiry = &v
			case 0x18:
				p.WillDelay = &v
			case 0x27:
				if v == 0 {
					return p, ErrProtocol
				}
				p.MaximumPacketSize = &v
			}
		case 0x21, 0x22, 0x23:
			v := f.uint16()
			switch id {
			case 0x21:
				if v == 0 {
					return p, ErrProtocol
				}
				p.ReceiveMaximum = &v
			case 0x22:
				p.TopicAliasMaximum = &v
			case 0x23:
				p.TopicAlias = &v
			}
		case 0x0B:
			v := f.varint()
			if v == 0 {
				return p, ErrProtocol
			}
			p.SubscriptionIDs = append(p.SubscriptionIDs, v)
		case 0x03:
			p.ContentType = f.utf8text(maxPropertyString)
		case 0x08:
			p.ResponseTopic = f.utf8text(MaxTopicBytes)
		case 0x09:
			p.CorrelationData = f.binary(maxPropertyString)
		case 0x15:
			p.AuthMethod = f.utf8text(maxPropertyString)
		case 0x16:
			p.AuthData = f.binary(maxPropertyString)
		case 0x1C:
			p.ServerReference = f.utf8text(maxPropertyString)
		case 0x1F:
			p.ReasonString = f.utf8text(maxPropertyString)
		case 0x26:
			key := f.utf8text(maxPropertyString)
			value := f.utf8text(maxPropertyString)
			if len(p.User) >= maxUserProperties {
				return p, ErrPacket
			}
			p.User = append(p.User, [2]string{key, value})
		default:
			return p, ErrProtocol
		}
	}
	if f.err != nil || f.position != end {
		return p, ErrPacket
	}
	return p, nil
}

// decodeConnectV5 reads an MQTT 5 CONNECT body after its protocol level.
func decodeConnectV5(body []byte) (Packet, error) {
	p := Packet{Kind: Connect, Version: 5}
	flags := body[7]
	if flags&0x01 != 0 {
		return Packet{}, ErrPacket
	}
	p.CleanSession = flags&0x02 != 0
	p.Will = flags&0x04 != 0
	if !p.Will && flags&0x38 != 0 || flags&0x18 == 0x18 {
		return Packet{}, ErrPacket
	}
	p.KeepAlive = binary.BigEndian.Uint16(body[8:10])
	f := fields{body: body, position: 10}
	var err error
	if p.Props, err = f.properties(connectProperties); err != nil {
		return Packet{}, err
	}
	p.ClientID = f.utf8text(MaxBodyBytes)
	if f.err != nil {
		return Packet{}, ErrPacket
	}
	if p.ClientID != "" && !identifier(p.ClientID) {
		return Packet{}, ErrClientID
	}
	if p.Will {
		// Read whole so the framing is checked; the service refuses wills.
		if _, err := f.properties(willProperties); err != nil {
			return Packet{}, err
		}
		_ = f.utf8text(MaxTopicBytes)
		_ = f.binary(MaxPayloadBytes)
	}
	if flags&0x80 != 0 {
		p.Username = f.utf8text(MaxCredentialBytes)
	}
	if flags&0x40 != 0 {
		p.Password, p.HasPassword = string(f.binary(MaxCredentialBytes)), true
	}
	if f.err != nil || f.position != len(body) {
		return Packet{}, ErrPacket
	}
	return p, nil
}

// ReadV5 consumes exactly one MQTT 5 packet after CONNECT.
func ReadV5(input io.Reader) (Packet, error) {
	var one [1]byte
	if _, err := io.ReadFull(input, one[:]); err != nil {
		return Packet{}, err
	}
	header := one[0]
	kind := Kind(header >> 4)
	switch {
	case kind == Publish:
	case header == 0x40, header == 0x82, header == 0xa2, header == 0xc0, header == 0xe0, header == 0xf0:
	default:
		return Packet{}, ErrPacket
	}
	size := 0
	for index := 0; ; index++ {
		if _, err := io.ReadFull(input, one[:]); err != nil {
			return Packet{}, err
		}
		value := one[0]
		size |= int(value&127) << (7 * index)
		if size > MaxBodyBytesV5 || index == 3 && value&128 != 0 {
			return Packet{}, ErrPacket
		}
		if value&128 == 0 {
			break
		}
	}
	body := make([]byte, size)
	defer clear(body)
	if _, err := io.ReadFull(input, body); err != nil {
		return Packet{}, err
	}
	return decodeV5(header, body)
}

func decodeV5(header byte, body []byte) (Packet, error) {
	p := Packet{Kind: Kind(header >> 4), Version: 5}
	f := fields{body: body}
	var err error
	switch p.Kind {
	case Ping:
		if len(body) != 0 {
			return Packet{}, ErrPacket
		}
		return p, nil
	case Disconnect:
		if len(body) > 0 {
			p.Reason = f.byte1()
		}
		if len(body) > 1 {
			if p.Props, err = f.properties(disconnectProperties); err != nil {
				return Packet{}, err
			}
		}
	case PubAck:
		p.ID = f.uint16()
		if len(body) > 2 {
			p.Reason = f.byte1()
		}
		if len(body) > 3 {
			if p.Props, err = f.properties(pubackProperties); err != nil {
				return Packet{}, err
			}
		}
		if p.ID == 0 {
			return Packet{}, ErrPacket
		}
	case Publish:
		p.Duplicate, p.QoS, p.Retain = header&8 != 0, (header>>1)&3, header&1 != 0
		if p.QoS == 3 || p.QoS == 0 && p.Duplicate {
			return Packet{}, ErrPacket
		}
		p.Topic = f.utf8text(MaxTopicBytes)
		if p.QoS > 0 {
			p.ID = f.uint16()
			if f.err == nil && p.ID == 0 {
				return Packet{}, ErrPacket
			}
		}
		if p.Props, err = f.properties(publishProperties); err != nil {
			return Packet{}, err
		}
		if f.err != nil || len(body)-f.position > MaxPayloadBytes {
			return Packet{}, ErrPacket
		}
		p.Payload = string(body[f.position:])
		f.position = len(body)
	case Subscribe:
		p.ID = f.uint16()
		if p.Props, err = f.properties(subscribeProperties); err != nil {
			return Packet{}, err
		}
		for f.err == nil && f.position < len(body) {
			filter := f.utf8text(MaxFilterBytes)
			options := f.byte1()
			if f.err != nil {
				break
			}
			if options&0xc0 != 0 || options&3 == 3 || (options>>4)&3 == 3 || filter == "" || len(p.Subscriptions) >= maxFilters {
				return Packet{}, ErrProtocol
			}
			p.Subscriptions = append(p.Subscriptions, Subscription{Filter: filter, QoS: options & 3, NoLocal: options&4 != 0, RetainAsPublished: options&8 != 0, RetainHandling: (options >> 4) & 3})
		}
		if f.err == nil && (p.ID == 0 || len(p.Subscriptions) == 0) {
			return Packet{}, ErrProtocol
		}
	case Unsubscribe:
		p.ID = f.uint16()
		if p.Props, err = f.properties(unsubscribeProperties); err != nil {
			return Packet{}, err
		}
		for f.err == nil && f.position < len(body) {
			filter := f.utf8text(MaxFilterBytes)
			if f.err == nil && (filter == "" || len(p.Filters) >= maxFilters) {
				return Packet{}, ErrProtocol
			}
			p.Filters = append(p.Filters, filter)
		}
		if f.err == nil && (p.ID == 0 || len(p.Filters) == 0) {
			return Packet{}, ErrProtocol
		}
	case AuthPacket:
		// Enhanced authentication is not offered; the service answers it
		// with a protocol error.
		return p, nil
	default:
		return Packet{}, ErrPacket
	}
	if f.err != nil || f.position != len(body) {
		return Packet{}, ErrPacket
	}
	return p, nil
}

// encoder builds an MQTT 5 packet body.
type encoder struct{ b []byte }

func (e *encoder) byte1(v byte)    { e.b = append(e.b, v) }
func (e *encoder) uint16(v uint16) { e.b = binary.BigEndian.AppendUint16(e.b, v) }
func (e *encoder) uint32(v uint32) { e.b = binary.BigEndian.AppendUint32(e.b, v) }
func (e *encoder) text(v string)   { e.uint16(uint16(len(v))); e.b = append(e.b, v...) }
func (e *encoder) binary(v []byte) { e.uint16(uint16(len(v))); e.b = append(e.b, v...) }
func (e *encoder) varint(v int)    { e.b = appendVarint(e.b, v) }
func (e *encoder) raw(v []byte)    { e.b = append(e.b, v...) }
func appendVarint(b []byte, v int) []byte {
	for {
		digit := byte(v % 128)
		v /= 128
		if v > 0 {
			digit |= 128
		}
		b = append(b, digit)
		if v == 0 {
			return b
		}
	}
}

// encode writes the set properties, with their length first.
func (p Properties) encode(e *encoder) {
	var body encoder
	opt8 := func(id byte, v *byte) {
		if v != nil {
			body.byte1(id)
			body.byte1(*v)
		}
	}
	opt16 := func(id byte, v *uint16) {
		if v != nil {
			body.byte1(id)
			body.uint16(*v)
		}
	}
	opt32 := func(id byte, v *uint32) {
		if v != nil {
			body.byte1(id)
			body.uint32(*v)
		}
	}
	str := func(id byte, v string) {
		if v != "" {
			body.byte1(id)
			body.text(v)
		}
	}
	opt8(0x01, p.PayloadFormat)
	opt32(0x02, p.MessageExpiry)
	str(0x03, p.ContentType)
	str(0x08, p.ResponseTopic)
	if p.CorrelationData != nil {
		body.byte1(0x09)
		body.binary(p.CorrelationData)
	}
	for _, id := range p.SubscriptionIDs {
		body.byte1(0x0B)
		body.varint(id)
	}
	opt32(0x11, p.SessionExpiry)
	str(0x12, p.AssignedClientID)
	opt16(0x13, p.ServerKeepAlive)
	str(0x1A, p.ResponseInfo)
	str(0x1F, p.ReasonString)
	opt16(0x21, p.ReceiveMaximum)
	opt16(0x22, p.TopicAliasMaximum)
	opt8(0x24, p.MaximumQoS)
	opt8(0x25, p.RetainAvailable)
	for _, pair := range p.User {
		body.byte1(0x26)
		body.text(pair[0])
		body.text(pair[1])
	}
	opt32(0x27, p.MaximumPacketSize)
	opt8(0x28, p.WildcardAvailable)
	opt8(0x29, p.SubIDAvailable)
	opt8(0x2A, p.SharedAvailable)
	e.varint(len(body.b))
	e.raw(body.b)
}

// packetV5 frames a body under a fixed header.
func packetV5(header byte, body []byte) []byte {
	out := appendVarint([]byte{header}, len(body))
	return append(out, body...)
}

// EncodedSize is the size of a whole packet with this body length.
func EncodedSize(body int) int { return len(appendVarint([]byte{0}, body)) + body }

// WriteConnackV5 accepts (reason 0) or refuses a connection.
func WriteConnackV5(out io.Writer, sessionPresent bool, reason byte, props Properties) error {
	var e encoder
	if sessionPresent && reason == 0 {
		e.byte1(1)
	} else {
		e.byte1(0)
	}
	e.byte1(reason)
	props.encode(&e)
	return write(out, packetV5(0x20, e.b))
}

// PublishV5 is an encoded PUBLISH, for a sender that checks its size
// against the client's Maximum Packet Size before writing it.
func PublishV5(topic string, id uint16, qos byte, duplicate bool, props Properties, payload []byte) []byte {
	var e encoder
	e.text(topic)
	if qos > 0 {
		e.uint16(id)
	}
	props.encode(&e)
	e.raw(payload)
	header := byte(0x30) | qos<<1
	if duplicate {
		header |= 8
	}
	return packetV5(header, e.b)
}

// WritePacket writes an already encoded packet.
func WritePacket(out io.Writer, packet []byte) error { return write(out, packet) }

// WritePubackV5 acknowledges a QoS 1 publication with a reason code.
func WritePubackV5(out io.Writer, id uint16, reason byte, props Properties) error {
	if id == 0 {
		return ErrPacket
	}
	var e encoder
	e.uint16(id)
	e.byte1(reason)
	props.encode(&e)
	return write(out, packetV5(0x40, e.b))
}

// WriteSubackV5 answers a SUBSCRIBE with a reason code per filter.
func WriteSubackV5(out io.Writer, id uint16, reasons []byte, props Properties) error {
	var e encoder
	e.uint16(id)
	props.encode(&e)
	e.raw(reasons)
	return write(out, packetV5(0x90, e.b))
}

// WriteUnsubackV5 answers an UNSUBSCRIBE with a reason code per filter.
func WriteUnsubackV5(out io.Writer, id uint16, reasons []byte) error {
	var e encoder
	e.uint16(id)
	Properties{}.encode(&e)
	e.raw(reasons)
	return write(out, packetV5(0xb0, e.b))
}

// WriteDisconnectV5 ends a connection with a reason code.
func WriteDisconnectV5(out io.Writer, reason byte, props Properties) error {
	var e encoder
	e.byte1(reason)
	props.encode(&e)
	return write(out, packetV5(0xe0, e.b))
}
