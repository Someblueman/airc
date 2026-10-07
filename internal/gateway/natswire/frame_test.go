package natswire

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestIndependentFrames(t *testing.T) {
	cases := []struct {
		wire string
		want Frame
	}{
		{"pub airc.send airc.result 5\r\na\x00\r\nb\r\n", Frame{Command: "PUB", Subject: "airc.send", Reply: "airc.result", Payload: []byte("a\x00\r\nb")}},
		{"MSG airc.result sid 0\r\n\r\n", Frame{Command: "MSG", Subject: "airc.result", SID: "sid", Payload: []byte{}}},
		{"msg airc.result sid airc.reply 1\r\nx\r\n", Frame{Command: "MSG", Subject: "airc.result", SID: "sid", Reply: "airc.reply", Payload: []byte("x")}},
		{"SUB\tairc.result\t42\r\n", Frame{Command: "SUB", Subject: "airc.result", SID: "42"}},
		{"UNSUB 42\r\n", Frame{Command: "UNSUB", SID: "42"}},
		{"CONNECT {\"auth_token\":\"secret\",\"verbose\":false}\r\n", Frame{Command: "CONNECT", JSON: []byte(`{"auth_token":"secret","verbose":false}`)}},
		{"INFO {\"tls_required\":true,\"headers\":false}\r\n", Frame{Command: "INFO", JSON: []byte(`{"tls_required":true,"headers":false}`)}},
		{"-ERR 'Authorization Violation'\r\n", Frame{Command: "-ERR", Error: "Authorization Violation"}},
		{"PING\r\n", Frame{Command: "PING"}}, {"PONG\r\n", Frame{Command: "PONG"}}, {"+OK\r\n", Frame{Command: "+OK"}},
	}
	for _, tc := range cases {
		t.Run(tc.want.Command+tc.want.Subject, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tc.wire + "PING\r\n"))
			got, err := Read(r)
			if err != nil || !equal(got, tc.want) {
				t.Fatalf("frame mismatch: %v", err)
			}
			if next, err := Read(r); err != nil || next.Command != "PING" {
				t.Fatalf("pipeline lost: %v", err)
			}
			encoded, err := Encode(got)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Read(bufio.NewReader(bytes.NewReader(encoded)))
			if err != nil || !equal(decoded, tc.want) {
				t.Fatalf("encoded frame mismatch: %v", err)
			}
		})
	}
}

func TestExactEncoding(t *testing.T) {
	n := uint32(7)
	for _, tc := range []struct {
		f    Frame
		wire string
	}{
		{Frame{Command: "PUB", Subject: "a", Reply: "r", Payload: []byte("é")}, "PUB a r 2\r\né\r\n"},
		{Frame{Command: "MSG", Subject: "a", SID: "1", Payload: []byte("\x00")}, "MSG a 1 1\r\n\x00\r\n"},
		{Frame{Command: "UNSUB", SID: "s", MaxMessages: &n}, "UNSUB s 7\r\n"},
	} {
		b, err := Encode(tc.f)
		if err != nil || string(b) != tc.wire {
			t.Fatalf("encoding: %q %v", b, err)
		}
		got, err := Read(bufio.NewReader(strings.NewReader(tc.wire)))
		if err != nil || !equal(got, tc.f) {
			t.Fatalf("decoding: %v", err)
		}
	}
}

func TestFramingRefusals(t *testing.T) {
	for _, s := range []string{
		"PING\n", "PING\rx", "PING x\r\n", "\r\n", " PING\r\n", "HPUB a 0 0\r\n", "HMSG a s 0 0\r\n",
		"SUB a queue sid\r\n", "SUB a.* sid\r\n", "SUB a.> sid\r\n", "SUB a..b sid\r\n", "SUB .a sid\r\n", "SUB a. sid\r\n", "SUB é sid\r\n",
		"UNSUB s -1\r\n", "UNSUB s 4294967296\r\n", "UNSUB s 1 x\r\n", "UNSUB\r\n",
		"PUB a -1\r\n", "PUB a +1\r\n", "PUB a 32769\r\n", "PUB a 999999999999\r\n", "PUB a 1\r\nxXX", "PUB a 1\r\n", "PUB a 1\r\nx\r", "PUB a * 0\r\n\r\n",
		"MSG a 1\r\n", "MSG a s 0 x\r\n", "CONNECT []\r\n", "CONNECT null\r\n", "CONNECT {} {}\r\n", "CONNECT {\"a\":1,\"a\":2}\r\n", "CONNECT {\"a\":\"\\ud800\"}\r\n",
		"-ERR unquoted\r\n", "-ERR 'bad'quote'\r\n", "-ERR 'bad\ttext'\r\n", "PING\x00\r\n", "-ERR '\xff'\r\n",
		"SUB a " + strings.Repeat("s", MaxSID+1) + "\r\n", "SUB " + strings.Repeat("a", MaxSubject+1) + " s\r\n", strings.Repeat("A", MaxLine+1) + "\r\n",
	} {
		if _, err := Read(bufio.NewReader(strings.NewReader(s))); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestEveryTruncation(t *testing.T) {
	for _, s := range []string{"PUB a r 5\r\nhello\r\n", "MSG a sid 3\r\nabc\r\n", "CONNECT {}\r\n", "SUB a sid\r\n"} {
		for i := range len(s) {
			if _, err := Read(bufio.NewReader(strings.NewReader(s[:i]))); err == nil {
				t.Fatalf("accepted truncation %d of %q", i, s)
			}
		}
	}
	if _, err := Read(bufio.NewReader(strings.NewReader(""))); err != io.EOF {
		t.Fatalf("empty stream: %v", err)
	}
}
