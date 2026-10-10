package rediswire

import (
	"bufio"
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestRESP2EncoderRefusesIrrelevantFields(t *testing.T) {
	for _, v := range []Value{
		{}, {Type: '>'}, {Type: Simple, Integer: 1}, {Type: Simple, Null: true}, {Type: Simple, Array: []Value{}}, {Type: Integer, Bytes: []byte{}}, {Type: Bulk, Null: true, Bytes: []byte{}}, {Type: Array, Null: true, Array: []Value{}}, {Type: Bulk, Array: []Value{}}, {Type: Array, Bytes: []byte{}}, {Type: Simple, Bytes: []byte("a\r\nb")}, {Type: Bulk, Bytes: make([]byte, MaxBulk+1)}, {Type: Array, Array: make([]Value, MaxElements+1)},
	} {
		if b, err := Encode(v); !errors.Is(err, ErrFrame) || b != nil {
			t.Fatalf("shape accepted %+v: %v", v, err)
		}
	}
}

func FuzzRESP2RoundTrip(f *testing.F) {
	for _, wire := range []string{"+OK\r\n", "$0\r\n\r\n", "*3\r\n$7\r\nmessage\r\n$1\r\nr\r\n$1\r\nx\r\n", "*-1\r\n", ":-1\r\n"} {
		f.Add([]byte(wire))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := Read(bufio.NewReader(bytes.NewReader(data)))
		if err != nil {
			return
		}
		wire, err := Encode(v)
		if err != nil || len(wire) > MaxFrame {
			t.Fatalf("decoded value cannot encode: %v", err)
		}
		got, err := Read(bufio.NewReader(bytes.NewReader(wire)))
		if err != nil || !reflect.DeepEqual(v, got) {
			t.Fatalf("round trip changed value: %v", err)
		}
	})
}
