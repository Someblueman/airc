package amqpwire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMessageIndependentSections(t *testing.T) {
	wire := []byte{0, 0x53, 0x70, 0xc0, 2, 1, 0x41, // durable header
		0, 0x53, 0x73, 0xc0, 4, 1, 0xa1, 1, 'i', // properties: message ID
		0, 0x53, 0x75, 0xa0, 2, 'h', 'i', 0, 0x53, 0x75, 0xa0, 1, '!'}
	m, err := DecodeMessage(wire)
	if err != nil || string(m.Body) != "hi!" || m.Header[0].Number != 1 || string(m.Properties[0].Data) != "i" {
		t.Fatalf("%+v %v", m, err)
	}
	clear(wire)
	if string(m.Body) != "hi!" || string(m.Properties[0].Data) != "i" {
		t.Fatal("borrowed message")
	}
	symbol := append([]byte{0, 0xa3, 16}, []byte("amqp:data:binary")...)
	symbol = append(symbol, 0xa0, 1, 'x')
	if m, err := DecodeMessage(symbol); err != nil || string(m.Body) != "x" {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestMessageRefusesOrderingTypesAndTruncation(t *testing.T) {
	data := []byte{0, 0x53, 0x75, 0xa0, 1, 'x'}
	header := []byte{0, 0x53, 0x70, 0x45}
	for _, wire := range [][]byte{
		nil, header, append(append([]byte{}, data...), header...),
		append(append(append([]byte{}, header...), header...), data...),
		{0, 0x53, 0x75, 0xa1, 1, 'x'}, {0, 0x53, 0x77, 0xa1, 1, 'x'},
		{0, 0x53, 0x70, 0xc0, 2, 1, 0x50},
		{0, 0x53, 0x73, 0xc0, 3, 1, 0x52, 1},
		{0, 0x53, 0x71, 0xc1, 3, 2, 0x41, 0x40},
		append(append([]byte{0, 0x53, 0x74, 0xc1, 5, 2, 0xa3, 1, 'k', 0x40}, data...), 0),
	} {
		if _, err := DecodeMessage(wire); err == nil {
			t.Fatalf("accepted %x", wire)
		}
	}
	for n := 0; n < len(data); n++ {
		if _, err := DecodeMessage(data[:n]); err == nil {
			t.Fatalf("prefix %d", n)
		}
	}
	if _, err := DecodeMessage(append(append([]byte{}, data...), 0x99)); err == nil {
		t.Fatal("trailing bytes")
	}
}

func TestMessageBodyAndSectionLimits(t *testing.T) {
	for _, size := range []int{MaxMessageBody, MaxMessageBody + 1} {
		wire := make([]byte, 8+size)
		copy(wire, []byte{0, 0x53, 0x75, 0xb0})
		binary.BigEndian.PutUint32(wire[4:8], uint32(size))
		for i := 8; i < len(wire); i++ {
			wire[i] = 'x'
		}
		m, err := DecodeMessage(wire)
		if (size == MaxMessageBody) != (err == nil) {
			t.Fatalf("size=%d err=%v", size, err)
		}
		if err == nil && len(m.Body) != size {
			t.Fatal("body size")
		}
	}
	for _, count := range []int{MaxMessageSections, MaxMessageSections + 1} {
		wire := bytes.Repeat([]byte{0, 0x53, 0x75, 0xa0, 1, 'x'}, count)
		_, err := DecodeMessage(wire)
		if (count == MaxMessageSections) != (err == nil) {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	wire, err := EncodeDataMessage([]byte("x"))
	expected := []byte{0, 0x80, 0, 0, 0, 0, 0, 0, 0, 0x75, 0xb0, 0, 0, 0, 1, 'x'}
	if err != nil || !bytes.Equal(wire, expected) {
		t.Fatalf("wire=%x err=%v", wire, err)
	}
	for _, body := range [][]byte{nil, make([]byte, MaxMessageBody+1)} {
		if wire, err := EncodeDataMessage(body); wire != nil || err == nil {
			t.Fatal("invalid output body")
		}
	}
}

func TestMessageUUIDAndTimestampProperties(t *testing.T) {
	uuid := append([]byte{0x98}, bytes.Repeat([]byte{0x11}, 16)...)
	fields := append([]byte{}, uuid...)
	fields = append(fields, bytes.Repeat([]byte{0x40}, 7)...)
	fields = append(fields, 0x83, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	wire := append([]byte{0, 0x53, 0x73, 0xc0, byte(len(fields) + 1), 9}, fields...)
	wire = append(wire, 0, 0x53, 0x75, 0xa0, 1, 'x')
	m, err := DecodeMessage(wire)
	if err != nil || len(m.Properties[0].Data) != 16 || int64(m.Properties[8].Number) != -1 {
		t.Fatalf("%+v err=%v", m, err)
	}
	for _, input := range [][]byte{uuid, fields[len(fields)-9:]} {
		value, n, err := DecodeValue(input)
		if err != nil || n != len(input) {
			t.Fatal(err)
		}
		encoded, err := EncodeValue(value, MaxFrameBytes)
		if err != nil || !bytes.Equal(encoded, input) {
			t.Fatalf("%x %v", encoded, err)
		}
		for n := 0; n < len(input); n++ {
			if _, _, err := DecodeValue(input[:n]); err == nil {
				t.Fatal("truncated fixed value")
			}
		}
	}
}

func FuzzDecodeMessage(f *testing.F) {
	f.Add([]byte{0, 0x53, 0x75, 0xa0, 1, 'x'})
	f.Fuzz(func(t *testing.T, wire []byte) {
		m, err := DecodeMessage(wire)
		if err == nil && (len(m.Body) == 0 || len(m.Body) > MaxMessageBody) {
			t.Fatal("body bound")
		}
	})
}
