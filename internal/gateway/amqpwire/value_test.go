package amqpwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestValueIndependentVectors(t *testing.T) {
	for _, test := range []struct {
		wire   []byte
		code   byte
		number uint64
		data   string
	}{
		{[]byte{0x40}, 0x40, 0, ""},
		{[]byte{0x41}, 0x56, 1, ""},
		{[]byte{0x56, 0}, 0x56, 0, ""},
		{[]byte{0x50, 255}, 0x50, 255, ""},
		{[]byte{0x60, 1, 2}, 0x60, 258, ""},
		{[]byte{0x43}, 0x70, 0, ""},
		{[]byte{0x52, 7}, 0x70, 7, ""},
		{[]byte{0x70, 0, 0, 1, 0}, 0x70, 256, ""},
		{[]byte{0x44}, 0x80, 0, ""},
		{[]byte{0x53, 9}, 0x80, 9, ""},
		{[]byte{0x80, 0, 0, 0, 0, 0, 0, 1, 0}, 0x80, 256, ""},
		{[]byte{0xa1, 2, 0xc3, 0xa9}, 0xa1, 0, "é"},
		{[]byte{0xb3, 0, 0, 0, 1, 'x'}, 0xa3, 0, "x"},
		{[]byte{0xa0, 1, 255}, 0xa0, 0, string([]byte{255})},
	} {
		wire := append(append([]byte{}, test.wire...), 0x99)
		v, n, err := DecodeValue(wire)
		if err != nil || n != len(test.wire) || v.Code != test.code || v.Number != test.number || string(v.Data) != test.data {
			t.Fatalf("wire=%x value=%+v n=%d err=%v", wire, v, n, err)
		}
		clear(wire)
		if string(v.Data) != test.data {
			t.Fatal("borrowed input")
		}
		for n := 0; n < len(test.wire); n++ {
			if _, _, err := DecodeValue(test.wire[:n]); err == nil {
				t.Fatalf("accepted prefix %x", test.wire[:n])
			}
		}
	}
}

func TestValueDescribedCollections(t *testing.T) {
	// AMQP close performative: ulong descriptor, list0.
	v, n, err := DecodeValue([]byte{0, 0x53, 0x18, 0x45})
	if err != nil || n != 4 || v.Descriptor.Code != 0x80 || v.Descriptor.Number != 0x18 || v.Children[0].Code != 0xc0 {
		t.Fatalf("%+v %v", v, err)
	}
	for _, wire := range [][]byte{
		{0xc0, 3, 2, 0x41, 0x40},
		{0xd0, 0, 0, 0, 6, 0, 0, 0, 2, 0x41, 0x40},
	} {
		v, n, err := DecodeValue(wire)
		if err != nil || n != len(wire) || len(v.Children) != 2 || v.Children[0].Number != 1 {
			t.Fatalf("%x %+v %v", wire, v, err)
		}
	}
	for _, wire := range [][]byte{
		{0xe0, 6, 2, 0xa3, 1, 'a', 1, 'b'},
		{0xf0, 0, 0, 0, 9, 0, 0, 0, 2, 0xa3, 1, 'a', 1, 'b'},
	} {
		v, n, err := DecodeValue(wire)
		if err != nil || n != len(wire) || v.Code != 0xe0 || len(v.Children) != 2 || string(v.Children[1].Data) != "b" {
			t.Fatalf("%x %+v %v", wire, v, err)
		}
	}
	mapWire := []byte{0xc1, 5, 2, 0xa3, 1, 'k', 0x40}
	if v, _, err := DecodeValue(mapWire); err != nil || v.Code != 0xc1 {
		t.Fatal(err)
	}
}

func TestValueMalformedAndLimits(t *testing.T) {
	for _, wire := range [][]byte{
		{0x56, 2}, {0xa1, 1, 255}, {0xa3, 1, 128}, {0xb0, 255, 255, 255, 255},
		{0xa1, 3, 0xef, 0xbb, 0xbf}, {0xb1, 0, 0, 0, 3, 0xef, 0xbb, 0xbf},
		{0xc0, 2, 0, 0x40}, {0xc0, 1, 1}, {0xc1, 2, 1, 0x40},
		{0xc1, 3, 2, 0x40, 0x40},             // null map key
		{0xc1, 5, 4, 0x41, 0x40, 0x41, 0x40}, // duplicate map key
		{0xe0, 1, 0}, {0xe0, 3, 1, 0xa3, 2}, {0xe0, 2, 0, 0xa1},
		{0, 0x52, 1, 0x45}, // reserved uint descriptor
		{0xd0, 0, 0, 0, 4, 255, 255, 255, 255},
	} {
		if _, _, err := DecodeValue(wire); !errors.Is(err, ErrValue) {
			t.Fatalf("accepted %x: %v", wire, err)
		}
	}
	list := func(count int) []byte {
		wire := make([]byte, 9+count)
		wire[0] = 0xd0
		binary.BigEndian.PutUint32(wire[1:5], uint32(4+count))
		binary.BigEndian.PutUint32(wire[5:9], uint32(count))
		copy(wire[9:], bytes.Repeat([]byte{0x40}, count))
		return wire
	}
	if _, _, err := DecodeValue(list(MaxValueCount)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeValue(list(MaxValueCount + 1)); err == nil {
		t.Fatal("count bound")
	}
	wire := []byte{0x40}
	for n := 1; n <= MaxValueDepth; n++ {
		wire = append([]byte{0xc0, byte(1 + len(wire)), 1}, wire...)
		_, _, err := DecodeValue(wire)
		if (n < MaxValueDepth) != (err == nil) {
			t.Fatalf("depth %d err=%v", n, err)
		}
	}
	if _, _, err := DecodeValue(make([]byte, MaxFrameBytes+1)); err == nil {
		t.Fatal("byte bound")
	}
}

func FuzzDecodeValue(f *testing.F) {
	f.Add([]byte{0, 0x53, 0x18, 0x45})
	f.Add([]byte{0xe0, 4, 1, 0xa3, 1, 'x'})
	f.Fuzz(func(t *testing.T, wire []byte) {
		_, n, err := DecodeValue(wire)
		if err == nil && (n <= 0 || n > len(wire) || n > MaxFrameBytes) {
			t.Fatalf("consumed %d/%d", n, len(wire))
		}
	})
}
