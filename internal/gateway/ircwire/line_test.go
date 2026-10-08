package ircwire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestServerTagsWholeBodyAndOwnedResult(t *testing.T) {
	raw := []byte("@account=alice;+account=forged;msgid=OpaqueID;time=2026-10-08T05:00:00.000Z;future?key=value :nick!user@host PRIVMSG #room : exact 世界  \r\n")
	c, err := Parse(raw)
	if err != nil || c.Name != "PRIVMSG" || c.Prefix != "nick!user@host" || len(c.Params) != 1 || c.Params[0] != "#room" || c.Trailing != " exact 世界  " || !c.HasTrailing {
		t.Fatalf("whole command %+v %v", c, err)
	}
	if c.Tags["account"] != "alice" || c.Tags["+account"] != "forged" || c.Tags["msgid"] != "OpaqueID" || c.Tags["future?key"] != "value" {
		t.Fatal(c.Tags)
	}
	clear(raw)
	if c.Trailing != " exact 世界  " || c.Tags["account"] != "alice" {
		t.Fatal("input alias")
	}
}

func TestTagEscapesLastValueAndCase(t *testing.T) {
	c, err := Parse([]byte("@account=first;account=last;Account=distinct;empty=;flag;esc=a\\:b\\sc\\\\d\\re\\nf\\q\\ PING :x\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Tags["account"] != "last" || c.Tags["Account"] != "distinct" || c.Tags["empty"] != "" || c.Tags["flag"] != "" || c.Tags["esc"] != "a;b c\\d\re\nfq" {
		t.Fatal(c.Tags)
	}
}

func TestReaderBoundariesAndNoPartialCommand(t *testing.T) {
	raw := "PING :one\r\nPONG :two\r\n"
	r := NewReader(strings.NewReader(raw))
	for _, want := range []string{"one", "two"} {
		c, err := r.Read()
		if err != nil || c.Trailing != want {
			t.Fatal(c, err)
		}
	}
	if c, err := r.Read(); !errors.Is(err, io.EOF) || c.Name != "" {
		t.Fatal(c, err)
	}
	for i := 0; i < len("PING :whole\r\n"); i++ {
		c, err := NewReader(strings.NewReader("PING :whole\r\n"[:i])).Read()
		if err == nil || c.Name != "" || c.Tags != nil {
			t.Fatalf("partial %d %+v %v", i, c, err)
		}
	}
	for _, r := range []*Reader{nil, NewReader(nil), NewReader(bytes.NewReader(bytes.Repeat([]byte{'x'}, MaxWire+2)))} {
		c, err := r.Read()
		if err == nil || c.Name != "" {
			t.Fatal(c, err)
		}
	}
}

func TestMalformedLinesRefuseWhole(t *testing.T) {
	cases := []string{"", "\r\n", "PING\n", "PING\r", "PING :a\x00b\r\n", "PING :a\rb\r\n", "@ PING\r\n", "@x;;y PING\r\n", "@x=\xff PING\r\n", ": PING\r\n", "PI?NG\r\n", "12\r\n", "@a=x @b=y PING\r\n", "CMD" + strings.Repeat(" p", 16) + "\r\n"}
	for _, raw := range cases {
		c, err := Parse([]byte(raw))
		if !errors.Is(err, ErrLine) || c.Name != "" || c.Tags != nil {
			t.Fatalf("%q %+v %v", raw, c, err)
		}
	}
}
