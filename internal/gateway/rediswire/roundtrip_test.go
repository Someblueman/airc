package rediswire

import (
	"bufio"
	"bytes"
	"math"
	"reflect"
	"testing"
)

func TestRESP2TypesNullsBinaryAndPipeline(t *testing.T) {
	for _, tc := range []struct {
		wire  string
		value Value
	}{
		{"+OK\r\n", Value{Type: Simple, Bytes: []byte("OK")}},
		{"-ERR refused\r\n", Value{Type: Error, Bytes: []byte("ERR refused")}},
		{":-9223372036854775808\r\n", Value{Type: Integer, Integer: math.MinInt64}},
		{":9223372036854775807\r\n", Value{Type: Integer, Integer: math.MaxInt64}},
		{"$-1\r\n", Value{Type: Bulk, Null: true}},
		{"$0\r\n\r\n", Value{Type: Bulk, Bytes: []byte{}}},
		{"*0\r\n", Value{Type: Array, Array: []Value{}}},
		{"*-1\r\n", Value{Type: Array, Null: true}},
		{"$6\r\n\x00\r\n世\r\n", Value{Type: Bulk, Bytes: []byte("\x00\r\n世")}},
		{"*3\r\n$7\r\nmessage\r\n$7\r\nrequest\r\n$4\r\nbody\r\n", Value{Type: Array, Array: []Value{{Type: Bulk, Bytes: []byte("message")}, {Type: Bulk, Bytes: []byte("request")}, {Type: Bulk, Bytes: []byte("body")}}}},
	} {
		t.Run(tc.wire, func(t *testing.T) {
			r := bufio.NewReader(bytes.NewBufferString(tc.wire + ":1\r\n"))
			got, err := Read(r)
			if err != nil || !reflect.DeepEqual(got, tc.value) {
				t.Fatalf("decode: %+v %v", got, err)
			}
			wire, err := Encode(got)
			if err != nil || string(wire) != tc.wire {
				t.Fatalf("encode %q %v", wire, err)
			}
			if next, err := Read(r); err != nil || next.Type != Integer || next.Integer != 1 {
				t.Fatal("pipeline consumed", err)
			}
		})
	}
	for _, wire := range []string{":+1\r\n", ":0001\r\n"} {
		if v, err := Read(bufio.NewReader(bytes.NewBufferString(wire))); err != nil || v.Integer != 1 {
			t.Fatal("valid decimal", err)
		}
	}
}

func TestRESP2TruncationAndOwnedPayload(t *testing.T) {
	for _, wire := range []string{"+OK\r\n", ":123\r\n", "$6\r\nabc\x00\r\n\r\n", "*2\r\n$0\r\n\r\n:1\r\n"} {
		for n := range len(wire) {
			if _, err := Read(bufio.NewReader(bytes.NewBufferString(wire[:n]))); err == nil {
				t.Fatalf("truncation accepted %q", wire[:n])
			}
		}
	}
	r := bufio.NewReaderSize(bytes.NewBufferString("$3\r\nabc\r\n$3\r\ndef\r\n"), 16)
	first, err := Read(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(r); err != nil {
		t.Fatal(err)
	}
	if string(first.Bytes) != "abc" || cap(first.Bytes) != len(first.Bytes) {
		t.Fatal("payload aliases reader/trailer")
	}
}
