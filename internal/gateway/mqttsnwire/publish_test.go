package mqttsnwire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Independent test encoder, including valid extended small-message framing.
func publication(payload []byte, extended bool) []byte {
	header := 1
	if extended || len(payload)+7 > 255 {
		header = 3
	}
	packet := make([]byte, header+6+len(payload))
	if header == 3 {
		packet[0] = 1
		binary.BigEndian.PutUint16(packet[1:3], uint16(len(packet)))
	} else {
		packet[0] = byte(len(packet))
	}
	copy(packet[header:], []byte{0x0c, 0x61, 0, 7, 0, 0})
	copy(packet[header+6:], payload)
	return packet
}

func TestDecode(t *testing.T) {
	for _, size := range []int{1, 248, 249, MaxPayloadBytes} {
		for _, extended := range []bool{false, true} {
			packet := publication(bytes.Repeat([]byte{0xff}, size), extended)
			got, err := Decode(packet)
			if err != nil || got.TopicID != 7 || got.Duplicate || got.Payload != string(packet[len(packet)-size:]) {
				t.Fatalf("size=%d extended=%v: %+v %v", size, extended, got, err)
			}
			clear(packet)
			if got.Payload != string(bytes.Repeat([]byte{0xff}, size)) {
				t.Fatal("payload aliases input")
			}
		}
	}
	packet := publication([]byte("hello"), false)
	packet[2] = 0xe1
	got, err := Decode(packet)
	if err != nil || !got.Duplicate {
		t.Fatalf("transport metadata: %+v %v", got, err)
	}
}

func TestRefusal(t *testing.T) {
	packet := publication([]byte("hello"), true)
	for size := 0; size < len(packet); size++ {
		if _, err := Decode(packet[:size]); err == nil {
			t.Fatalf("accepted truncation %d", size)
		}
	}
	for _, mutate := range []func([]byte) []byte{
		func(p []byte) []byte { p[4] = 0x21; return p },           // QoS 1
		func(p []byte) []byte { p[4] = 0x62; return p },           // short topic
		func(p []byte) []byte { p[4] |= 0x10; return p },          // retained
		func(p []byte) []byte { p[4] |= 4; return p },             // clean session
		func(p []byte) []byte { p[4] |= 8; return p },             // Will
		func(p []byte) []byte { p[3] = 4; return p },              // CONNECT
		func(p []byte) []byte { p[7], p[8] = 255, 255; return p }, // nonzero message ID
		func(p []byte) []byte { p[5], p[6] = 0, 0; return p },
		func(p []byte) []byte { p[5], p[6] = 255, 255; return p },
		func(p []byte) []byte { p[2]++; return p },
		func(p []byte) []byte { return append(p, 0) },
		func(p []byte) []byte { return append(p, p...) },
	} {
		if _, err := Decode(mutate(bytes.Clone(packet))); err == nil {
			t.Fatal("accepted malformed/unsupported packet")
		}
	}
	for _, payload := range [][]byte{nil, make([]byte, MaxPayloadBytes+1)} {
		if _, err := Decode(publication(payload, true)); err == nil {
			t.Fatal("accepted payload outside bounds")
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(publication([]byte("hello"), false))
	f.Add(publication([]byte("hello"), true))
	f.Fuzz(func(t *testing.T, packet []byte) {
		got, err := Decode(packet)
		if err == nil && (got.TopicID == 0 || got.TopicID == 0xffff || len(got.Payload) == 0 || len(got.Payload) > MaxPayloadBytes) {
			t.Fatal("invalid successful publication")
		}
	})
}
