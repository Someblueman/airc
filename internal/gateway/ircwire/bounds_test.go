package ircwire

import (
	"strings"
	"testing"
)

func TestSeparateTagAndMessageLimits(t *testing.T) {
	message := "PING :" + strings.Repeat("x", MaxMessage-8) + "\r\n"
	tags := "@k=" + strings.Repeat("x", MaxTags-4) + " "
	for _, raw := range []string{message, tags + message} {
		c, err := Parse([]byte(raw))
		if err != nil || len(c.Trailing) != MaxMessage-8 {
			t.Fatal(len(raw), c, err)
		}
	}
	for _, raw := range []string{message[:len(message)-2] + "x\r\n", tags + " " + message, tags[:len(tags)-1] + "x " + message, "@k=x " + strings.Repeat("x", MaxMessage-1) + "\r\n"} {
		if c, err := Parse([]byte(raw)); err == nil || c.Name != "" {
			t.Fatal("oversized declaration", len(raw), c, err)
		}
	}
	if c, err := NewReader(strings.NewReader(tags + message)).Read(); err != nil || c.Tags["k"] != strings.Repeat("x", MaxTags-4) {
		t.Fatal("maximum complete line", err)
	}
}

func TestClientEncodingWholeBodyAndLimits(t *testing.T) {
	body := " exact 世界  "
	wire, err := Encode("privmsg", []string{"#room"}, &body)
	if err != nil || string(wire) != "PRIVMSG #room : exact 世界  \r\n" {
		t.Fatal(string(wire), err)
	}
	c, err := Parse(wire)
	if err != nil || c.Trailing != body {
		t.Fatal(c, err)
	}
	empty := ""
	wire, err = Encode("TOPIC", []string{"#room"}, &empty)
	if err != nil || string(wire) != "TOPIC #room :\r\n" {
		t.Fatal(string(wire), err)
	}
	body = strings.Repeat("x", MaxMessage-len("PING :\r\n"))
	if wire, err = Encode("PING", nil, &body); err != nil || len(wire) != MaxMessage {
		t.Fatal(len(wire), err)
	}
	body += "x"
	for _, tc := range []struct {
		name   string
		params []string
		body   *string
	}{
		{"PING", nil, &body}, {"P?NG", nil, nil}, {"CMD", []string{""}, nil}, {"CMD", []string{":x"}, nil}, {"CMD", []string{"x y"}, nil}, {"CMD", []string{"x\r\n"}, nil}, {"CMD", make([]string, 16), nil},
	} {
		if wire, err := Encode(tc.name, tc.params, tc.body); err == nil || wire != nil {
			t.Fatal(tc, wire, err)
		}
	}
	for _, s := range []string{"x\r", "x\n", "x\x00"} {
		if wire, err := Encode("PING", nil, &s); err == nil || wire != nil {
			t.Fatal(wire, err)
		}
	}
}
