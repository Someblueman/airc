package mqttwire

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

func identifier(value string) bool {
	const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	return len(value) > 0 && len(value) <= 64 && strings.Trim(value, allowed) == ""
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
		// Credentials and a clean session are mandatory. Will, retained
		// publications and persistent MQTT sessions are outside this profile.
		if body[7] != 0xc2 {
			return Packet{}, ErrPacket
		}
		p.KeepAlive = binary.BigEndian.Uint16(body[8:10])
		if p.KeepAlive < 1 || p.KeepAlive > 60 {
			return Packet{}, ErrPacket
		}
		f := fields{body: body, position: 10}
		p.ClientID = f.text(MaxBodyBytes)
		if f.err != nil || !utf8.ValidString(p.ClientID) || strings.ContainsRune(p.ClientID, 0) {
			return Packet{}, ErrPacket
		}
		if !identifier(p.ClientID) {
			return Packet{}, ErrClientID
		}
		p.Username, p.Password = f.text(64), f.text(64)
		if f.err != nil || f.position != len(body) || !identifier(p.Username) || len(p.Password) != 64 || strings.Trim(p.Password, "0123456789abcdefABCDEF") != "" {
			return Packet{}, ErrPacket
		}
	case Publish:
		f := fields{body: body}
		p.Topic = f.text(MaxTopicBytes)
		p.ID = f.uint16()
		if f.err != nil || !ValidTopic(p.Topic) || p.ID == 0 || len(body)-f.position < 1 || len(body)-f.position > MaxPayloadBytes {
			return Packet{}, ErrPacket
		}
		p.Payload = string(body[f.position:])
	default:
		return Packet{}, ErrPacket
	}
	return p, nil
}
