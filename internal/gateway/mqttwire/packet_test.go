package mqttwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func packetBytes(header byte, body []byte) []byte {
	out := []byte{header}
	n := len(body)
	for {
		value := byte(n % 128)
		n /= 128
		if n > 0 {
			value |= 128
		}
		out = append(out, value)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}
func fieldBytes(out []byte, text string) []byte {
	return append(binary.BigEndian.AppendUint16(out, uint16(len(text))), text...)
}
func connectBytes() []byte {
	b := []byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0xc2, 0, 30}
	b = fieldBytes(b, "producer")
	b = fieldBytes(b, "producer")
	return fieldBytes(b, strings.Repeat("a", 64))
}
func publishBytes(topic, payload string, id uint16) []byte {
	b := fieldBytes(nil, topic)
	b = binary.BigEndian.AppendUint16(b, id)
	return append(b, payload...)
}

func TestPacketStreamBoundariesAndFields(t *testing.T) {
	wire := packetBytes(0x10, connectBytes())
	wire = append(wire, packetBytes(0x3a, publishBytes("airc/in/producer", "opaque.signed.payload", 65535))...)
	wire = append(wire, 0xc0, 0, 0xe0, 0)
	original := bytes.Clone(wire)
	r := bytes.NewReader(wire)
	p, err := Read(r)
	if err != nil || p.Kind != Connect || p.ClientID != "producer" || p.Username != "producer" || p.Password != strings.Repeat("a", 64) || p.KeepAlive != 30 {
		t.Fatal(p, err)
	}
	p, err = Read(r)
	if err != nil || p.Kind != Publish || !p.Duplicate || p.ID != 65535 || p.Topic != "airc/in/producer" || p.Payload != "opaque.signed.payload" {
		t.Fatal(p, err)
	}
	for _, kind := range []Kind{Ping, Disconnect} {
		p, err = Read(r)
		if err != nil || p.Kind != kind {
			t.Fatal(p, err)
		}
	}
	if r.Len() != 0 || !bytes.Equal(wire, original) {
		t.Fatal("packet boundaries or input modified")
	}
}

func TestConnectProfileRefusals(t *testing.T) {
	for name, mutate := range map[string]func([]byte) []byte{
		"protocol": func(b []byte) []byte { b[2] = 'm'; return b }, "version": func(b []byte) []byte { b[6] = 5; return b },
		"persistent": func(b []byte) []byte { b[7] &= ^byte(2); return b }, "will": func(b []byte) []byte { b[7] |= 4; return b }, "reserved": func(b []byte) []byte { b[7] |= 1; return b }, "credentials": func(b []byte) []byte { b[7] = 2; return b },
		"keepalive_zero": func(b []byte) []byte { b[9] = 0; return b }, "keepalive_large": func(b []byte) []byte { b[9] = 61; return b },
		"client_utf8":   func(b []byte) []byte { b[12] = 0xff; return b },
		"client_nul":    func(b []byte) []byte { b[12] = 0; return b },
		"client_policy": func(b []byte) []byte { b[12] = '.'; return b },
		"client_empty": func(b []byte) []byte {
			out := fieldBytes(b[:10], "")
			out = fieldBytes(out, "producer")
			return fieldBytes(out, strings.Repeat("a", 64))
		},
		"client_large": func(b []byte) []byte {
			out := fieldBytes(b[:10], strings.Repeat("a", 65))
			out = fieldBytes(out, "producer")
			return fieldBytes(out, strings.Repeat("a", 64))
		}, "password": func(b []byte) []byte { b[len(b)-1] = 'g'; return b }, "trailing": func(b []byte) []byte { return append(b, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Read(bytes.NewReader(packetBytes(0x10, mutate(connectBytes()))))
			if err == nil {
				t.Fatal("accepted unsupported CONNECT")
			}
			if name == "version" && !errors.Is(err, ErrVersion) {
				t.Fatal(err)
			}
			if strings.HasPrefix(name, "client_") && name != "client_utf8" && name != "client_nul" && !errors.Is(err, ErrClientID) {
				t.Fatal(err)
			}
			if name == "client_utf8" && !errors.Is(err, ErrPacket) {
				t.Fatal(err)
			}
		})
	}
}

func TestPublishLimitsAndMalformedPackets(t *testing.T) {
	exact := packetBytes(0x32, publishBytes(strings.Repeat("t", MaxTopicBytes), strings.Repeat("x", MaxPayloadBytes), 1))
	p, err := Read(bytes.NewReader(exact))
	if err != nil || len(p.Payload) != MaxPayloadBytes {
		t.Fatal(err)
	}
	for _, wire := range [][]byte{
		packetBytes(0x32, publishBytes("topic", "", 1)), packetBytes(0x32, publishBytes("topic", "body", 0)), packetBytes(0x32, publishBytes("topic/#", "body", 1)), packetBytes(0x32, publishBytes("$SYS/topic", "body", 1)), packetBytes(0x32, publishBytes("topic\x00", "body", 1)), packetBytes(0x32, publishBytes(strings.Repeat("t", 129), "body", 1)), packetBytes(0x32, publishBytes("topic", strings.Repeat("x", MaxPayloadBytes+1), 1)),
		{0x30, 0}, {0x34, 0}, {0x36, 0}, {0x33, 0}, {0x82, 0}, {0xc1, 0}, {0xc0, 1, 0}, {0xe0, 1, 0}, {0xc0, 0x80, 0}, {0x32, 0xff, 0xff, 0xff, 0xff},
	} {
		if _, err := Read(bytes.NewReader(wire)); err == nil {
			t.Fatalf("accepted malformed wire %x", wire[:min(8, len(wire))])
		}
	}
	// The declared body limit is refused without consuming any body bytes.
	declared := packetBytes(0x32, make([]byte, MaxBodyBytes+1))
	r := bytes.NewReader(declared)
	_, err = Read(r)
	if !errors.Is(err, ErrPacket) || r.Len() != MaxBodyBytes+1 {
		t.Fatal("oversize consumed body", r.Len(), err)
	}
	for _, wire := range [][]byte{packetBytes(0x10, connectBytes()), packetBytes(0x32, publishBytes("topic", "body", 1))} {
		for n := range len(wire) {
			if _, err := Read(bytes.NewReader(wire[:n])); err == nil {
				t.Fatal("accepted truncated packet", n)
			}
		}
	}
}

func FuzzRead(f *testing.F) {
	f.Add(packetBytes(0x10, connectBytes()))
	f.Add(packetBytes(0x32, publishBytes("topic", "token", 1)))
	f.Add([]byte{0xc0, 0})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, wire []byte) {
		p, err := Read(bytes.NewReader(wire))
		if err == nil && p.Kind == Publish && (p.ID == 0 || len(p.Payload) > MaxPayloadBytes || !ValidTopic(p.Topic)) {
			t.Fatal("invalid decoded publication")
		}
	})
}
