package amqpwire

import (
	"bytes"
	"testing"
)

func TestTransportIndependentWireVectors(t *testing.T) {
	for _, test := range []struct {
		kind byte
		body []byte
	}{
		{0x10, []byte{0, 0x53, 0x10, 0xc0, 4, 1, 0xa1, 1, 'c'}},
		{0x11, []byte{0, 0x53, 0x11, 0xc0, 5, 4, 0x40, 0x43, 0x43, 0x43}},
		{0x12, []byte{0, 0x53, 0x12, 0xc0, 6, 3, 0xa1, 1, 'l', 0x43, 0x41}},
		{0x13, []byte{0, 0x53, 0x13, 0xc0, 5, 4, 0x40, 0x43, 0x43, 0x43}},
		{0x14, []byte{0, 0x53, 0x14, 0xc0, 2, 1, 0x43}},
		{0x15, []byte{0, 0x53, 0x15, 0xc0, 3, 2, 0x41, 0x43}},
		{0x16, []byte{0, 0x53, 0x16, 0xc0, 2, 1, 0x43}},
		{0x17, []byte{0, 0x53, 0x17, 0x45}},
		{0x18, []byte{0, 0x53, 0x18, 0x45}},
	} {
		p, err := DecodePerformative(Frame{Type: AMQP, Body: test.body})
		if err != nil || p.Kind != test.kind {
			t.Fatalf("%x %+v err=%v", test.body, p, err)
		}
		for n := 1; n < len(test.body); n++ {
			if _, err := DecodePerformative(Frame{Body: test.body[:n]}); err == nil {
				t.Fatalf("prefix %x", test.body[:n])
			}
		}
		body := append(append([]byte{}, test.body...), 0xaa)
		p, err = DecodePerformative(Frame{Body: body})
		if test.kind == 0x14 {
			if err != nil || !bytes.Equal(p.Payload, []byte{0xaa}) {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("non-transfer payload")
		}
	}
	symbol := append([]byte{0, 0xa3, 15}, []byte("amqp:close:list")...)
	symbol = append(symbol, 0x45)
	if p, err := DecodePerformative(Frame{Body: symbol}); err != nil || p.Kind != 0x18 {
		t.Fatal(err)
	}
}

func TestTransportMandatoryFieldsAndRefusals(t *testing.T) {
	for kind := byte(0x10); kind <= 0x16; kind++ {
		if _, err := EncodePerformative(Performative{Kind: kind}, 0, 512); err == nil {
			t.Fatalf("missing %x", kind)
		}
	}
	for _, p := range []Performative{
		{Kind: 0x10, Fields: []Value{{Code: 0xa1}, {Code: 0x40}, {Code: 0x70, Number: 511}}},
		{Kind: 0x12, Fields: []Value{{Code: 0xa1}, {Code: 0x70}, {Code: 0x56}}}, // sender needs initial count
		{Kind: 0x12, Fields: []Value{{Code: 0xa1}, {Code: 0x70}, {Code: 0x56, Number: 1}, {Code: 0x50, Number: 3}}},
		{Kind: 0x13, Fields: []Value{{Code: 0x40}, {Code: 0x70}, {Code: 0x70}, {Code: 0x70}, {Code: 0x40}, {Code: 0x70}}},
		{Kind: 0x14, Fields: []Value{{Code: 0x70}, {Code: 0x40}, {Code: 0xa0, Data: make([]byte, 33)}}},
		{Kind: 0x14, Fields: []Value{{Code: 0x70}, {Code: 0x40}, {Code: 0x40}, {Code: 0x70, Number: 1}}},
		{Kind: 0x18, Payload: []byte{1}},
	} {
		if _, err := EncodePerformative(p, 0, 512); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
	if _, err := DecodePerformative(Frame{Type: SASL}); err == nil {
		t.Fatal("SASL frame")
	}
	if _, err := DecodePerformative(Frame{Channel: 1, Body: []byte{0, 0x53, 0x10, 0xc0, 3, 1, 0xa1, 0}}); err == nil {
		t.Fatal("open channel")
	}
}

func TestTransportIndependentEncodingAndPayloadOwnership(t *testing.T) {
	p := Performative{Kind: 0x14, Fields: []Value{{Code: 0x70}}, Payload: []byte{'x'}}
	frame, err := EncodePerformative(p, 7, 512)
	expected := []byte{0, 0x80, 0, 0, 0, 0, 0, 0, 0, 0x14, 0xd0, 0, 0, 0, 9, 0, 0, 0, 1, 0x70, 0, 0, 0, 0, 'x'}
	if err != nil || frame.Channel != 7 || !bytes.Equal(frame.Body, expected) {
		t.Fatalf("%x err=%v", frame.Body, err)
	}
	p.Payload[0] = 'z'
	if frame.Body[len(frame.Body)-1] != 'x' {
		t.Fatal("borrowed output")
	}
	decoded, err := DecodePerformative(frame)
	if err != nil {
		t.Fatal(err)
	}
	clear(frame.Body)
	if string(decoded.Payload) != "x" {
		t.Fatal("borrowed input")
	}
	if frame, err := EncodePerformative(Performative{}, 9, 512); err != nil || len(frame.Body) != 0 || frame.Channel != 9 {
		t.Fatal(err)
	}
	for _, size := range []int{480, 481} {
		// Prefix 10, list header9, uint5 = 24 bytes, plus payload.
		_, err := EncodePerformative(Performative{Kind: 0x14, Fields: []Value{{Code: 0x70}}, Payload: make([]byte, size)}, 0, 512)
		if (size == 480) != (err == nil) {
			t.Fatalf("size=%d err=%v", size, err)
		}
	}
}

func FuzzDecodePerformative(f *testing.F) {
	f.Add([]byte{0, 0x53, 0x18, 0x45})
	f.Add([]byte{0, 0x53, 0x14, 0xc0, 2, 1, 0x43, 'x'})
	f.Fuzz(func(t *testing.T, body []byte) {
		p, err := DecodePerformative(Frame{Body: body})
		if err == nil && p.Kind != 0 && (p.Kind < 0x10 || p.Kind > 0x18 || p.Kind != 0x14 && len(p.Payload) != 0) {
			t.Fatal("invalid performative")
		}
	})
}
