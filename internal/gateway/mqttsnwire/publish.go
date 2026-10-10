// Package mqttsnwire decodes the gateway's connectionless MQTT-SN 1.2 profile.
// Authentication and predefined topic authority belong to the caller.
package mqttsnwire

import (
	"encoding/binary"
	"errors"
)

const (
	MaxPayloadBytes = 32768
	MaxPacketBytes  = MaxPayloadBytes + 9
)

var ErrPacket = errors.New("unsupported or malformed MQTT-SN publication")

type Publish struct {
	TopicID   uint16
	Duplicate bool
	Payload   string
}

// Decode consumes exactly one datagram. It supports only non-retained QoS -1
// PUBLISH with a predefined topic ID. Both legal length encodings are accepted,
// including the extended encoding of a small packet. The wire message ID must
// be zero; DUP does not establish replay identity. Payload is copied.
func Decode(packet []byte) (Publish, error) {
	if len(packet) < 8 || len(packet) > MaxPacketBytes {
		return Publish{}, ErrPacket
	}
	header, length := 1, int(packet[0])
	if packet[0] == 1 {
		header, length = 3, int(binary.BigEndian.Uint16(packet[1:3]))
	}
	if length != len(packet) || len(packet)-header < 7 {
		return Publish{}, ErrPacket
	}
	body := packet[header:]
	// Flags: optional DUP, QoS -1 (11), no RETAIN/Will/CleanSession,
	// predefined topic ID (01). Other profiles require separate integration.
	if body[0] != 0x0c || body[1]&0x7f != 0x61 {
		return Publish{}, ErrPacket
	}
	topic := binary.BigEndian.Uint16(body[2:4])
	if topic == 0 || topic == 0xffff || binary.BigEndian.Uint16(body[4:6]) != 0 || len(body)-6 > MaxPayloadBytes {
		return Publish{}, ErrPacket
	}
	return Publish{TopicID: topic, Duplicate: body[1]&0x80 != 0, Payload: string(body[6:])}, nil
}
