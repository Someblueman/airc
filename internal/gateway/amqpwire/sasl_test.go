package amqpwire

import (
	"bytes"
	"testing"
)

func TestSASLIndependentWireVectors(t *testing.T) {
	for _, test := range []struct {
		kind byte
		body []byte
	}{
		{0x40, []byte{0, 0x53, 0x40, 0xc0, 8, 1, 0xa3, 5, 'P', 'L', 'A', 'I', 'N'}},
		{0x40, []byte{0, 0x53, 0x40, 0xc0, 11, 1, 0xe0, 8, 1, 0xa3, 5, 'P', 'L', 'A', 'I', 'N'}},
		{0x41, []byte{0, 0x53, 0x41, 0xc0, 8, 1, 0xa3, 5, 'P', 'L', 'A', 'I', 'N'}},
		{0x42, []byte{0, 0x53, 0x42, 0xc0, 3, 1, 0xa0, 0}},
		{0x43, []byte{0, 0x53, 0x43, 0xc0, 3, 1, 0xa0, 0}},
		{0x44, []byte{0, 0x53, 0x44, 0xc0, 3, 1, 0x50, 0}},
	} {
		p, err := DecodeSASL(Frame{Type: SASL, Body: test.body})
		if err != nil || p.Kind != test.kind {
			t.Fatalf("%x %+v err=%v", test.body, p, err)
		}
		for n := 0; n < len(test.body); n++ {
			if _, err := DecodeSASL(Frame{Type: SASL, Body: test.body[:n]}); err == nil {
				t.Fatalf("prefix %d", n)
			}
		}
		if _, err := DecodeSASL(Frame{Type: SASL, Body: append(append([]byte{}, test.body...), 0x40)}); err == nil {
			t.Fatal("trailing value")
		}
	}
	symbol := append([]byte{0, 0xa3, 22}, []byte("amqp:sasl-outcome:list")...)
	// Descriptor length is determined independently of the encoder.
	symbol[2] = byte(len("amqp:sasl-outcome:list"))
	symbol = append(symbol, 0xc0, 3, 1, 0x50, 0)
	if p, err := DecodeSASL(Frame{Type: SASL, Body: symbol}); err != nil || p.Kind != 0x44 {
		t.Fatal(err)
	}
}

func TestSASLIndependentOutputAndRefusals(t *testing.T) {
	frame, err := EncodeSASL(SASLPerformative{Kind: 0x44, Fields: []Value{{Code: 0x50, Number: 0}}})
	expected := []byte{0, 0x80, 0, 0, 0, 0, 0, 0, 0, 0x44, 0xd0, 0, 0, 0, 6, 0, 0, 0, 1, 0x50, 0}
	if err != nil || frame.Type != SASL || frame.Channel != 0 || !bytes.Equal(frame.Body, expected) {
		t.Fatalf("%+v err=%v", frame, err)
	}
	for _, p := range []SASLPerformative{
		{}, {Kind: 0x41}, {Kind: 0x40, Fields: []Value{{Code: 0xa3}}}, {Kind: 0x40, Fields: []Value{{Code: 0xe0}}},
		{Kind: 0x40, Fields: []Value{{Code: 0xe0, Children: []Value{{Code: 0xa3}}}}},
		{Kind: 0x41, Fields: []Value{{Code: 0x40}}}, {Kind: 0x42, Fields: []Value{{Code: 0xa1}}},
		{Kind: 0x44, Fields: []Value{{Code: 0x70}}}, {Kind: 0x44, Fields: []Value{{Code: 0x50, Number: 5}}},
		{Kind: 0x44, Fields: []Value{{Code: 0x50, Number: 1}, {Code: 0xa0}}},
		{Kind: 0x43, Fields: []Value{{Code: 0xa0}, {Code: 0x40}}},
	} {
		if _, err := EncodeSASL(p); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
	for _, frame := range []Frame{{Type: AMQP, Body: expected}, {Type: SASL, Channel: 1, Body: expected}, {Type: SASL}, {Type: SASL, Body: make([]byte, 505)}} {
		if _, err := DecodeSASL(frame); err == nil {
			t.Fatal("invalid frame")
		}
	}
}

func TestSASLExactOutboundLimitAndOptionalFields(t *testing.T) {
	// Described prefix 10 + list32 header 9 + binary32 prefix 5 = 24 bytes.
	for _, size := range []int{480, 481} {
		p := SASLPerformative{Kind: 0x43, Fields: []Value{{Code: 0xa0, Data: make([]byte, size)}}}
		frame, err := EncodeSASL(p)
		if (size == 480) != (err == nil) {
			t.Fatalf("size=%d err=%v", size, err)
		}
		if err == nil {
			if len(frame.Body) != 504 {
				t.Fatal("wrong size")
			}
			if _, err := DecodeSASL(frame); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := SASLPerformative{Kind: 0x41, Fields: []Value{{Code: 0xa3, Data: []byte("PLAIN")}, {Code: 0x40}, {Code: 0xa1, Data: []byte("host")}}}
	frame, err := EncodeSASL(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSASL(frame)
	if err != nil || len(decoded.Fields) != 3 {
		t.Fatal(err)
	}
}

func FuzzDecodeSASL(f *testing.F) {
	f.Add([]byte{0, 0x53, 0x44, 0xc0, 3, 1, 0x50, 0})
	f.Fuzz(func(t *testing.T, body []byte) {
		p, err := DecodeSASL(Frame{Type: SASL, Body: body})
		if err == nil && (p.Kind < 0x40 || p.Kind > 0x44 || len(p.Fields) == 0) {
			t.Fatal("invalid performative")
		}
	})
}
