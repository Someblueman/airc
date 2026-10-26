package zmqwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestLiteralFramesAndOwnership(t *testing.T) {
	source := []byte{1, 0, 0, 3, 'a', 'b', 'c', 99}
	r := bytes.NewReader(source)
	traffic, err := ReadTraffic(r)
	got := traffic.Body
	if err != nil || traffic.Ping || string(got) != "abc" || r.Len() != 1 {
		t.Fatalf("request %q %v remaining %d", got, err, r.Len())
	}
	source[4] = 'z'
	if string(got) != "abc" {
		t.Fatal("borrowed input")
	}
	reply, err := Reply(got)
	if err != nil || !bytes.Equal(reply, []byte{1, 0, 0, 3, 'a', 'b', 'c'}) {
		t.Fatalf("reply %x %v", reply, err)
	}
	got[0] = 'x'
	if reply[4] != 'a' {
		t.Fatal("borrowed reply")
	}
}

func TestFrameBounds(t *testing.T) {
	for _, size := range []int{0, 255, 256, MaxData} {
		body := bytes.Repeat([]byte{'x'}, size)
		wire, err := EncodeFrame(Frame{Body: body})
		if err != nil {
			t.Fatal(err)
		}
		if size < 256 && !bytes.Equal(wire[:2], []byte{0, byte(size)}) {
			t.Fatal("short header")
		}
		if size >= 256 && (wire[0] != 2 || binary.BigEndian.Uint64(wire[1:9]) != uint64(size)) {
			t.Fatal("long header")
		}
		got, err := ReadFrame(bytes.NewReader(wire))
		if err != nil || !bytes.Equal(got.Body, body) {
			t.Fatalf("size %d: %v", size, err)
		}
	}
	for _, size := range []uint64{MaxData + 1, ^uint64(0)} {
		wire := binary.BigEndian.AppendUint64([]byte{2}, size)
		if _, err := ReadFrame(bytes.NewReader(wire)); !errors.Is(err, ErrFrame) {
			t.Fatalf("size %d: %v", size, err)
		}
	}
	if _, err := EncodeFrame(Frame{Body: make([]byte, MaxData+1)}); err == nil {
		t.Fatal("oversized encode")
	}
	command := append([]byte{1, 'X'}, make([]byte, MaxCommand-2)...)
	if _, err := EncodeFrame(Frame{Command: true, Body: command}); err != nil {
		t.Fatal(err)
	}
	command = append(command, 0)
	if _, err := EncodeFrame(Frame{Command: true, Body: command}); err == nil {
		t.Fatal("oversized command")
	}
}

func TestMalformedFrames(t *testing.T) {
	for _, wire := range [][]byte{{8}, {5}, {7}, {4, 0}, {4, 1, 0}, {4, 2, 2, 'A'}, {4, 2, 1, '1'}} {
		if got, err := ReadFrame(bytes.NewReader(wire)); err == nil || got.Body != nil {
			t.Fatalf("accepted %x: %+v %v", wire, got, err)
		}
	}
	for _, wire := range [][]byte{{0, 3, 'a', 'b', 'c'}, {2, 0, 0, 0, 0, 0, 0, 1, 0}} {
		for i := 0; i < len(wire); i++ {
			if got, err := ReadFrame(bytes.NewReader(wire[:i])); err == nil || got.Body != nil {
				t.Fatalf("partial %x: %v", wire[:i], err)
			}
		}
	}
	if _, err := ReadFrame(nil); err == nil {
		t.Fatal("nil reader")
	}
	if _, err := EncodeFrame(Frame{More: true, Command: true, Body: []byte{1, 'X'}}); err == nil {
		t.Fatal("multipart command")
	}
}

func TestRequestRefusals(t *testing.T) {
	for _, wire := range [][]byte{{0, 0}, {1, 1, 'x'}, {1, 0, 0, 0}, {1, 0, 1, 1, 'x'}, {1, 0, 4, 2, 1, 'X'}} {
		if got, err := ReadTraffic(bytes.NewReader(wire)); err == nil || got.Body != nil {
			t.Fatalf("accepted %x: %+v %v", wire, got, err)
		}
	}
	wire := []byte{1, 0, 0, 3, 'a', 'b', 'c'}
	for i := 0; i < len(wire); i++ {
		if got, err := ReadTraffic(bytes.NewReader(wire[:i])); err == nil || got.Body != nil {
			t.Fatalf("partial request %d", i)
		}
	}
	if _, err := Reply(nil); err == nil {
		t.Fatal("empty reply")
	}
	if _, err := Reply(make([]byte, MaxData+1)); err == nil {
		t.Fatal("oversized reply")
	}
}

// ZMTP 3.1 heartbeats: a PING between messages is answered with a PONG
// echoing its context, and the next message is read normally.
func TestPingBetweenMessages(t *testing.T) {
	ping, err := EncodeFrame(Frame{Command: true, Body: append([]byte("\x04PING\x00\x0a"), "ctx"...)})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte{1, 0, 0, 2, 'h', 'i'}
	r := bytes.NewReader(append(ping, request...))
	first, err := ReadTraffic(r)
	if err != nil || !first.Ping || string(first.Context) != "ctx" {
		t.Fatalf("ping %+v %v", first, err)
	}
	pong, err := Pong(first.Context)
	if err != nil || !bytes.Equal(pong, []byte{4, 8, 4, 'P', 'O', 'N', 'G', 'c', 't', 'x'}) {
		t.Fatalf("pong %q %v", pong, err)
	}
	second, err := ReadTraffic(r)
	if err != nil || second.Ping || string(second.Body) != "hi" {
		t.Fatalf("request after ping %+v %v", second, err)
	}
	for _, bad := range [][]byte{[]byte("\x04PING\x00"), append([]byte("\x04PING\x00\x00"), make([]byte, 17)...), []byte("\x05ERROR\x00")} {
		wire, err := EncodeFrame(Frame{Command: true, Body: bad})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTraffic(bytes.NewReader(wire)); err == nil {
			t.Fatalf("accepted command %q", bad)
		}
	}
}
