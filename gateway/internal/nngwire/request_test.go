package nngwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestLiteralRequestAndReplyOwnership(t *testing.T) {
	wire := []byte{0, 0, 0, 0, 0, 0, 0, 7, 0x80, 0, 0, 42, 'a', 'b', 'c', 99}
	r := bytes.NewReader(wire)
	request, err := ReadRequest(r)
	if err != nil || request.Correlation != 0x8000002a || string(request.Body) != "abc" || r.Len() != 1 {
		t.Fatalf("request %+v %v remaining %d", request, err, r.Len())
	}
	wire[12] = 'z'
	if string(request.Body) != "abc" {
		t.Fatal("borrowed input")
	}
	reply, err := Reply(request.Correlation, request.Body)
	if err != nil || !bytes.Equal(reply, []byte{0, 0, 0, 0, 0, 0, 0, 7, 0x80, 0, 0, 42, 'a', 'b', 'c'}) {
		t.Fatalf("reply %x %v", reply, err)
	}
	request.Body[0] = 'x'
	if reply[12] != 'a' {
		t.Fatal("borrowed reply")
	}
}
func TestRequestTruncationAndDeclaredBounds(t *testing.T) {
	literal := []byte{0, 0, 0, 0, 0, 0, 0, 7, 0x80, 0, 0, 42, 'a', 'b', 'c'}
	for i := 0; i < len(literal); i++ {
		if got, err := ReadRequest(bytes.NewReader(literal[:i])); err == nil || got.Body != nil || got.Correlation != 0 {
			t.Fatalf("partial request %d: %+v %v", i, got, err)
		}
	}
	for _, size := range []uint64{0, 1, 4, MaxData + 5, ^uint64(0)} {
		wire := binary.BigEndian.AppendUint64(nil, size)
		if got, err := ReadRequest(bytes.NewReader(wire)); !errors.Is(err, ErrFrame) || got.Body != nil || got.Correlation != 0 {
			t.Fatalf("size %d: %+v %v", size, got, err)
		}
	}
	if _, err := ReadRequest(nil); err == nil {
		t.Fatal("nil reader")
	}
	for _, correlation := range []uint32{0, 1, 0x7fffffff} {
		wire := binary.BigEndian.AppendUint64(nil, 5)
		wire = binary.BigEndian.AppendUint32(wire, correlation)
		r := bytes.NewReader(append(wire, 'x'))
		if _, err := ReadRequest(r); !errors.Is(err, ErrFrame) || r.Len() != 1 {
			t.Fatalf("nonterminal backtrace %x: %v", correlation, err)
		}
	}
}
func TestApplicationAndCorrelationBounds(t *testing.T) {
	for _, size := range []int{1, 255, 256, MaxData} {
		for _, correlation := range []uint32{0x80000000, 0xffffffff} {
			body := bytes.Repeat([]byte{'x'}, size)
			wire, err := Reply(correlation, body)
			if err != nil || len(wire) != size+12 || binary.BigEndian.Uint64(wire[:8]) != uint64(size+4) {
				t.Fatalf("encode %d: %v", size, err)
			}
			request, err := ReadRequest(bytes.NewReader(wire))
			if err != nil || request.Correlation != correlation || !bytes.Equal(request.Body, body) {
				t.Fatalf("decode %d: %v", size, err)
			}
		}
	}
	for _, body := range [][]byte{nil, {}, make([]byte, MaxData+1)} {
		if got, err := Reply(0x80000001, body); err == nil || got != nil {
			t.Fatal("invalid reply size")
		}
	}
	if got, err := Reply(1, []byte("body")); err == nil || got != nil {
		t.Fatal("nonterminal reply identifier")
	}
}
