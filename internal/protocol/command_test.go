package protocol

import (
	"errors"
	"testing"
)

func TestParseTrailingAndPrefix(t *testing.T) {
	cmd, err := Parse(":alice!agent@localhost PRIVMSG #foo :hello world")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Prefix != "alice!agent@localhost" || cmd.Name != "PRIVMSG" || len(cmd.Params) != 1 || cmd.Params[0] != "#foo" || cmd.Trailing != "hello world" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseTags(t *testing.T) {
	cmd, err := Parse("@msgid=abc;time=2026-09-30T21:00:00Z;note=hello\\sworld :alice PRIVMSG bob :hello world")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Tags["msgid"] != "abc" || cmd.Tags["note"] != "hello world" || cmd.Tags["time"] == "" || cmd.Prefix != "alice" || cmd.Trailing != "hello world" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseMalformedBoundaries(t *testing.T) {
	for _, line := range []string{"", "@tag", ":", "PING\x00", string(make([]byte, MaxLineLength+1))} {
		if _, err := Parse(line); !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q) err = %v", line, err)
		}
	}
}
