package stompwire

import (
	"bufio"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFramesAndHeartbeatBoundaries(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\r\nSEND\r\ndestination:/in\r\ncontent-length:3\r\nx: first \\c value\r\nx:second\r\n\r\na\x00b\x00\nDISCONNECT\n\n\x00"))
	f, err := Read(r)
	if err != nil || f.Command != "" {
		t.Fatalf("heartbeat: %+v %v", f, err)
	}
	f, err = Read(r)
	if err != nil || f.Command != "SEND" || !bytes.Equal(f.Body, []byte{'a', 0, 'b'}) {
		t.Fatalf("body: %+v %v", f, err)
	}
	if value, ok := f.Header("x"); !ok || value != " first : value" {
		t.Fatalf("first value %q %v", value, ok)
	}
	if f, err = Read(r); err != nil || f.Command != "" {
		t.Fatalf("trailing heartbeat: %+v %v", f, err)
	}
	if f, err = Read(r); err != nil || f.Command != "DISCONNECT" {
		t.Fatalf("next frame: %+v %v", f, err)
	}
}

func TestHeaderEscapingAndConnectExceptions(t *testing.T) {
	golden, err := Encode(Frame{Command: "SEND", Headers: []Header{{"a:b", "x\ny\rz\\"}, {"content-length", "3"}}, Body: []byte{'a', 0, 'b'}})
	if err != nil || string(golden) != "SEND\na\\cb:x\\ny\\rz\\\\\ncontent-length:3\n\na\x00b\x00" {
		t.Fatalf("wire %q: %v", golden, err)
	}
	for _, command := range []string{"SEND", "STOMP", "MESSAGE", "CONNECT", "CONNECTED"} {
		t.Run(command, func(t *testing.T) {
			value := `literal\n:colon`
			if escaped(command) {
				value = "line\ncarriage\r:slash\\"
			}
			f := Frame{Command: command, Headers: []Header{{"x", value}}}
			wire, err := Encode(f)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Read(bufio.NewReader(bytes.NewReader(wire)))
			if err != nil || !reflect.DeepEqual(got.Headers, f.Headers) {
				t.Fatalf("headers %+v %v", got, err)
			}
		})
	}
}

func TestRefuseInvalidFrames(t *testing.T) {
	for name, wire := range map[string]string{
		"unknown-command": "UNKNOWN\n\n\x00", "bad-escape": "SEND\nx:\\t\n\n\x00",
		"dangling-escape": "SEND\nx:\\\n\n\x00", "empty-key": "SEND\n:value\n\n\x00",
		"invalid-utf8": "SEND\nx:\xff\n\n\x00", "nul-header": "SEND\nx:a\x00b\n\n\x00",
		"bare-cr": "SEND\nx:a\rb\n\n\x00", "signed-length": "SEND\ncontent-length:+1\n\na\x00",
		"overflow-length":  "SEND\ncontent-length:4294967296\n\n\x00",
		"excess-body":      "SEND\ncontent-length:32769\n\n\x00",
		"wrong-terminator": "SEND\ncontent-length:1\n\nab",
		"forbidden-body":   "ACK\n\nx\x00", "forbidden-declared-body": "ACK\ncontent-length:1\n\nx\x00",
		"header-count":      "SEND\n" + strings.Repeat("x:v\n", MaxHeaders+1) + "\n\x00",
		"line-limit":        "SEND\nx:" + strings.Repeat("v", MaxLine) + "\n\n\x00",
		"unterminated-body": "SEND\n\n" + strings.Repeat("x", MaxBody+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(bufio.NewReader(strings.NewReader(wire))); !errors.Is(err, ErrFrame) {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestExactLineBodyAndFrameLimits(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		wire := "SEND" + eol + "x:" + strings.Repeat("v", MaxLine-2) + eol + eol + strings.Repeat("b", MaxBody) + "\x00"
		f, err := Read(bufio.NewReader(strings.NewReader(wire)))
		if err != nil || len(f.Body) != MaxBody {
			t.Fatalf("exact limits %q: %v", eol, err)
		}
	}
	f := Frame{Command: "SEND", Body: bytes.Repeat([]byte{'b'}, MaxBody)}
	for i := 0; i < MaxHeaders; i++ {
		f.Headers = append(f.Headers, Header{"x", strings.Repeat("v", MaxLine-2)})
	}
	if _, err := Encode(f); !errors.Is(err, ErrFrame) {
		t.Fatalf("total encode bound %v", err)
	}
	wire := "SEND\n" + strings.Repeat("x:"+strings.Repeat("v", MaxLine-2)+"\n", MaxHeaders) + "\n" + string(f.Body) + "\x00"
	if _, err := Read(bufio.NewReader(strings.NewReader(wire))); !errors.Is(err, ErrFrame) {
		t.Fatalf("total decode bound %v", err)
	}
}

func TestEncodeRefusalsAndTruncation(t *testing.T) {
	for _, f := range []Frame{
		{Command: "ACK", Body: []byte("x")}, {Command: "SEND", Body: []byte{0}},
		{Command: "SEND", Headers: []Header{{"content-length", "2"}}, Body: []byte("x")},
		{Command: "CONNECT", Headers: []Header{{"bad:key", "x"}}},
		{Command: "CONNECTED", Headers: []Header{{"x", "bad\nvalue"}}},
		{Headers: []Header{{"x", "v"}}},
	} {
		if _, err := Encode(f); !errors.Is(err, ErrFrame) {
			t.Fatalf("encode %+v: %v", f, err)
		}
	}
	for _, wire := range []string{"SEND", "SEND\nx:", "SEND\n\nx", "SEND\ncontent-length:3\n\nab"} {
		if _, err := Read(bufio.NewReader(strings.NewReader(wire))); err == nil {
			t.Fatalf("accepted truncated %q", wire)
		}
	}
}
