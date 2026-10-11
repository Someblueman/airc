// Package mqttwire implements the bounded MQTT 3.1.1 publish-only ingress profile:
// QoS 0 and 1 publications, no subscriptions, wills or retained messages.
// Parsing does not authenticate, admit work, or acknowledge native acceptance.
package mqttwire

import (
	"errors"
	"io"
)

const MaxPayloadBytes = 32768
const MaxTopicBytes = 128
const MaxBodyBytes = MaxPayloadBytes + MaxTopicBytes + 4

// MaxCredentialBytes bounds CONNECT user names and passwords.
const MaxCredentialBytes = 256

var ErrPacket = errors.New("invalid or unsupported MQTT ingress packet")
var ErrVersion = errors.New("unsupported MQTT protocol level")
var ErrClientID = errors.New("invalid MQTT client identifier")

type Kind byte

const (
	Connect    Kind = 1
	Publish    Kind = 3
	Ping       Kind = 12
	Disconnect Kind = 14
)

type Packet struct {
	Kind                         Kind
	ClientID, Username, Password string
	HasPassword, CleanSession    bool
	KeepAlive                    uint16
	Topic, Payload               string
	// QoS is 0 (at most once, no acknowledgement) or 1 (PUBACK, with ID).
	QoS       byte
	ID        uint16
	Duplicate bool
}

// Read consumes exactly one packet. It never buffers the next packet, and
// refuses an oversized remaining length before allocating or reading its body.
func Read(input io.Reader) (Packet, error) {
	var one [1]byte
	if _, err := io.ReadFull(input, one[:]); err != nil {
		return Packet{}, err
	}
	header := one[0]
	// PUBLISH at QoS 0 or 1, never retained; QoS 2 is outside this profile.
	if header != 0x10 && header != 0x30 && header != 0x32 && header != 0x3a && header != 0xc0 && header != 0xe0 {
		return Packet{}, ErrPacket
	}
	size := 0
	for index := 0; ; index++ {
		if _, err := io.ReadFull(input, one[:]); err != nil {
			return Packet{}, err
		}
		value := one[0]
		size |= int(value&127) << (7 * index)
		if size > MaxBodyBytes || index == 3 && value&128 != 0 {
			return Packet{}, ErrPacket
		}
		if value&128 == 0 {
			if index > 0 && value == 0 {
				return Packet{}, ErrPacket
			}
			break
		}
	}
	if (header == 0xc0 || header == 0xe0) && size != 0 {
		return Packet{}, ErrPacket
	}
	body := make([]byte, size)
	defer clear(body)
	if _, err := io.ReadFull(input, body); err != nil {
		return Packet{}, err
	}
	return decode(header, body)
}
