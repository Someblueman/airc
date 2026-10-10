package natswire

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestExactBoundsAndEncoderRefusals(t *testing.T) {
	for _, f := range []Frame{
		{Command: "PUB", Subject: strings.Repeat("a", MaxSubject), Payload: bytes.Repeat([]byte{0xff}, MaxPayload)},
		{Command: "SUB", Subject: "a", SID: strings.Repeat("s", MaxSID)},
		{Command: "-ERR", Error: strings.Repeat("e", MaxLine-len("-ERR ''"))},
		{Command: "CONNECT", JSON: []byte(`{"a":"` + strings.Repeat("x", MaxLine-len("CONNECT ")-len(`{"a":""}`)) + `"}`)},
	} {
		b, err := Encode(f)
		if err != nil {
			t.Fatalf("exact bound: %v", err)
		}
		got, err := Read(bufio.NewReader(bytes.NewReader(b)))
		if err != nil || !equal(got, f) {
			t.Fatalf("exact decode: %v", err)
		}
	}
	for _, f := range []Frame{
		{Command: "PUB", Subject: "a", Payload: make([]byte, MaxPayload+1)},
		{Command: "SUB", Subject: "a", SID: strings.Repeat("s", MaxSID+1)},
		{Command: "PUB", Subject: strings.Repeat("a", MaxSubject+1)},
		{Command: "PING", Subject: "a"}, {Command: "SUB", Subject: "a", SID: "s", Payload: []byte("x")},
		{Command: "PUB", Subject: "a b"}, {Command: "-ERR", Error: "x\r\nPING"},
		{Command: "-ERR", Error: strings.Repeat("e", MaxLine-len("-ERR ''")+1)},
		{Command: "CONNECT", JSON: []byte("{}\r\nPING")},
	} {
		if _, err := Encode(f); err == nil {
			t.Fatalf("accepted invalid encoder frame %s", f.Command)
		}
	}
}

func TestOversizeLengthRefusedBeforePayload(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("PUB a 32769\r\nPING\r\n"))
	if _, err := Read(r); err == nil {
		t.Fatal("accepted oversize")
	}
	// Inspection only: a session owner must terminate on this error, not recover.
	if b, err := r.Peek(6); err != nil || string(b) != "PING\r\n" {
		t.Fatalf("consumed payload: %q %v", b, err)
	}
}

func FuzzRead(f *testing.F) {
	for _, s := range []string{"PUB a r 1\r\nx\r\n", "CONNECT {}\r\n", "SUB a s\r\n", "UNSUB s 7\r\n", "-ERR 'bad'\r\n", "PING\r\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxLine+MaxPayload+4 {
			return
		}
		got, err := Read(bufio.NewReader(bytes.NewReader(b)))
		if err != nil {
			return
		}
		encoded, err := Encode(got)
		if err != nil {
			t.Fatalf("parsed frame cannot encode: %v", err)
		}
		again, err := Read(bufio.NewReader(bytes.NewReader(encoded)))
		if err != nil || !equal(got, again) {
			t.Fatalf("round trip: %v", err)
		}
	})
}
