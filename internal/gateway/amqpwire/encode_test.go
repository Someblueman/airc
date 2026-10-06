package amqpwire

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeIndependentWireVectors(t *testing.T) {
	for _, test := range []struct {
		value Value
		wire  []byte
	}{
		{Value{Code: 0x40}, []byte{0x40}},
		{Value{Code: 0x56, Number: 1}, []byte{0x56, 1}},
		{Value{Code: 0x50, Number: 255}, []byte{0x50, 255}},
		{Value{Code: 0x60, Number: 258}, []byte{0x60, 1, 2}},
		{Value{Code: 0x70, Number: 256}, []byte{0x70, 0, 0, 1, 0}},
		{Value{Code: 0x80, Number: 256}, []byte{0x80, 0, 0, 0, 0, 0, 0, 1, 0}},
		{Value{Code: 0xa0, Data: []byte{255}}, []byte{0xb0, 0, 0, 0, 1, 255}},
		{Value{Code: 0xa1, Data: []byte("é")}, []byte{0xb1, 0, 0, 0, 2, 0xc3, 0xa9}},
		{Value{Code: 0xa3, Data: []byte("x")}, []byte{0xb3, 0, 0, 0, 1, 'x'}},
		{Value{Code: 0xc0, Children: []Value{{Code: 0x40}}}, []byte{0xd0, 0, 0, 0, 5, 0, 0, 0, 1, 0x40}},
		{Value{Code: 0xc1, Children: []Value{{Code: 0xa3, Data: []byte("k")}, {Code: 0x40}}}, []byte{0xd1, 0, 0, 0, 11, 0, 0, 0, 2, 0xb3, 0, 0, 0, 1, 'k', 0x40}},
		{Value{Code: 0xe0, Children: []Value{{Code: 0xa3, Data: []byte("x")}}}, []byte{0xf0, 0, 0, 0, 10, 0, 0, 0, 1, 0xb3, 0, 0, 0, 1, 'x'}},
		{Value{Descriptor: &Value{Code: 0x80, Number: 0x18}, Children: []Value{{Code: 0xc0}}}, []byte{0, 0x80, 0, 0, 0, 0, 0, 0, 0, 0x18, 0xd0, 0, 0, 0, 4, 0, 0, 0, 0}},
	} {
		wire, err := EncodeValue(test.value, len(test.wire))
		if err != nil || !bytes.Equal(wire, test.wire) {
			t.Fatalf("value=%+v wire=%x err=%v", test.value, wire, err)
		}
		if wire, err := EncodeValue(test.value, len(test.wire)-1); wire != nil || !errors.Is(err, ErrValue) {
			t.Fatalf("below limit: %x %v", wire, err)
		}
		decoded, n, err := DecodeValue(wire)
		if err != nil || n != len(wire) || decoded.Code != test.value.Code {
			t.Fatalf("decoded=%+v err=%v", decoded, err)
		}
	}
}

func TestEncodeRefusesInvalidShapesAndRanges(t *testing.T) {
	for _, value := range []Value{
		{}, {Code: 0x41}, {Code: 0x56, Number: 2}, {Code: 0x50, Number: 256}, {Code: 0x60, Number: 65536}, {Code: 0x70, Number: 1 << 32},
		{Code: 0x40, Number: 1}, {Code: 0x40, Data: []byte{1}}, {Code: 0x40, Children: []Value{{Code: 0x40}}},
		{Code: 0x40, Descriptor: &Value{Code: 0x80}}, {Code: 0xa1, Data: []byte{255}}, {Code: 0xa1, Data: []byte{0xef, 0xbb, 0xbf}},
		{Code: 0xa3, Data: []byte{128}}, {Code: 0xc1, Children: []Value{{Code: 0x40}}},
		{Code: 0xc1, Children: []Value{{Code: 0x40}, {Code: 0x40}}},
		{Code: 0xc1, Children: []Value{{Code: 0x50, Number: 1}, {Code: 0x40}, {Code: 0x50, Number: 1}, {Code: 0x40}}},
		{Code: 0xe0, Children: []Value{{Code: 0xa1}}}, {Descriptor: &Value{Code: 0x70}, Children: []Value{{Code: 0xc0}}},
	} {
		if wire, err := EncodeValue(value, MaxFrameBytes); wire != nil || !errors.Is(err, ErrValue) {
			t.Fatalf("accepted %+v: %x %v", value, wire, err)
		}
	}
	for _, limit := range []int{-1, 0, MaxFrameBytes + 1} {
		if wire, err := EncodeValue(Value{Code: 0x40}, limit); wire != nil || err == nil {
			t.Fatal("invalid limit")
		}
	}
}

func TestEncodeTreeBoundsAndOwnership(t *testing.T) {
	value := Value{Code: 0xa0, Data: bytes.Repeat([]byte{1}, MaxFrameBytes-5)}
	wire, err := EncodeValue(value, MaxFrameBytes)
	if err != nil || len(wire) != MaxFrameBytes {
		t.Fatal(err)
	}
	clear(value.Data)
	if wire[len(wire)-1] != 1 {
		t.Fatal("borrowed input")
	}
	value.Data = append(value.Data, 1)
	if _, err := EncodeValue(value, MaxFrameBytes); err == nil {
		t.Fatal("byte limit")
	}
	value = Value{Code: 0x40}
	for depth := 1; depth <= MaxValueDepth; depth++ {
		value = Value{Code: 0xc0, Children: []Value{value}}
		_, err := EncodeValue(value, MaxFrameBytes)
		if (depth < MaxValueDepth) != (err == nil) {
			t.Fatalf("depth=%d err=%v", depth, err)
		}
	}
	cycle := Value{Code: 0xc0, Children: make([]Value, 1)}
	cycle.Children[0] = cycle
	if _, err := EncodeValue(cycle, MaxFrameBytes); err == nil {
		t.Fatal("cycle")
	}
	list := func(n int) Value {
		v := Value{Code: 0xc0, Children: make([]Value, n)}
		for i := range v.Children {
			v.Children[i].Code = 0x40
		}
		return v
	}
	for _, leaves := range []int{1023, 1024} {
		value = Value{Code: 0xc0, Children: []Value{list(leaves), list(1022)}}
		_, err := EncodeValue(value, MaxFrameBytes)
		if (leaves == 1023) != (err == nil) {
			t.Fatalf("total node limit leaves=%d err=%v", leaves, err)
		}
	}
	if _, err := EncodeValue(list(MaxValueCount+1), MaxFrameBytes); err == nil {
		t.Fatal("count limit")
	}
}

func FuzzValueEncodeDecoded(f *testing.F) {
	f.Add([]byte{0, 0x53, 0x18, 0x45})
	f.Add([]byte{0xe0, 4, 1, 0xa3, 1, 'x'})
	f.Fuzz(func(t *testing.T, input []byte) {
		value, _, err := DecodeValue(input)
		if err != nil {
			return
		}
		wire, err := EncodeValue(value, MaxFrameBytes)
		if errors.Is(err, ErrValue) {
			return
		} // Wide encodings may exceed the byte ceiling.
		if err != nil {
			t.Fatal(err)
		}
		decoded, n, err := DecodeValue(wire)
		if err != nil || n != len(wire) || !equalValues(value, decoded) {
			t.Fatalf("round trip %x err=%v", wire, err)
		}
	})
}

func equalValues(a, b Value) bool {
	if a.Code != b.Code || a.Number != b.Number || !bytes.Equal(a.Data, b.Data) || len(a.Children) != len(b.Children) || (a.Descriptor == nil) != (b.Descriptor == nil) {
		return false
	}
	if a.Descriptor != nil && !equalValues(*a.Descriptor, *b.Descriptor) {
		return false
	}
	for i := range a.Children {
		if !equalValues(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}
