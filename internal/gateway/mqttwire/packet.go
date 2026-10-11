package mqttwire

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

// identifier accepts a bounded printable ASCII client identifier: a superset
// of the 1-23 alphanumerics MQTT 3.1.1 requires servers to accept, which
// covers the generated IDs of common clients.
func identifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// ValidTopic permits bounded literal application topics, without wildcards or
// reserved system topics. The service must separately verify topic authority.
func ValidTopic(value string) bool {
	const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-/."
	return len(value) > 0 && len(value) <= MaxTopicBytes && value[0] != '/' && strings.Trim(value, allowed) == ""
}

type fields struct {
	body     []byte
	position int
	err      error
}

func (f *fields) uint16() uint16 {
	if f.err != nil || len(f.body)-f.position < 2 {
		f.err = ErrPacket
		return 0
	}
	value := binary.BigEndian.Uint16(f.body[f.position:])
	f.position += 2
	return value
}
func (f *fields) text(limit int) string {
	size := int(f.uint16())
	if f.err != nil || size > limit || size > len(f.body)-f.position {
		f.err = ErrPacket
		return ""
	}
	value := string(f.body[f.position : f.position+size])
	f.position += size
	return value
}

func decode(header byte, body []byte) (Packet, error) {
	p := Packet{Kind: Kind(header >> 4), Duplicate: header&8 != 0}
	switch p.Kind {
	case Ping, Disconnect:
		return p, nil
	case Connect:
		if len(body) < 10 || string(body[:6]) != "\x00\x04MQTT" {
			return Packet{}, ErrPacket
		}
		if body[6] != 4 {
			return Packet{}, ErrVersion
		}
		// Will publications are outside this profile. A password needs a
		// user name (MQTT 3.1.1 section 3.1.2.9); both are optional. Without
		// persistent subscriptions or inflight server messages, a session
		// that asks to resume is equivalent to a clean one.
		flags := body[7]
		if flags&0x3d != 0 || flags&0x40 != 0 && flags&0x80 == 0 {
			return Packet{}, ErrPacket
		}
		p.CleanSession = flags&0x02 != 0
		p.KeepAlive = binary.BigEndian.Uint16(body[8:10])
		f := fields{body: body, position: 10}
		p.ClientID = f.text(MaxBodyBytes)
		if f.err != nil || !utf8.ValidString(p.ClientID) || strings.ContainsRune(p.ClientID, 0) {
			return Packet{}, ErrPacket
		}
		if p.ClientID == "" && !p.CleanSession || p.ClientID != "" && !identifier(p.ClientID) {
			return Packet{}, ErrClientID
		}
		if flags&0x80 != 0 {
			p.Username = f.text(MaxCredentialBytes)
		}
		if flags&0x40 != 0 {
			p.Password, p.HasPassword = f.text(MaxCredentialBytes), true
		}
		if f.err != nil || f.position != len(body) || !utf8.ValidString(p.Username) {
			return Packet{}, ErrPacket
		}
	case Publish:
		p.QoS = (header >> 1) & 3
		f := fields{body: body}
		p.Topic = f.text(MaxTopicBytes)
		if p.QoS == 1 {
			p.ID = f.uint16()
		}
		if f.err != nil || !ValidTopic(p.Topic) || p.QoS == 1 && p.ID == 0 || len(body)-f.position < 1 || len(body)-f.position > MaxPayloadBytes {
			return Packet{}, ErrPacket
		}
		p.Payload = string(body[f.position:])
	default:
		return Packet{}, ErrPacket
	}
	return p, nil
}
