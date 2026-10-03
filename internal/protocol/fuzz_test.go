package protocol

import (
	"strings"
	"testing"
)

// Parse sees every byte a client sends before authentication. It must never
// panic, and anything it accepts must stay within the documented limits.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"PRIVMSG #room :hello world",
		"@+airc/body=aGVsbG8;+airc/request-id=r1 PRIVMSG #room :hello",
		":nick!user@host JOIN #room",
		"@a=b\\:c\\s\\\\ CHAT :{\"action\":\"context\"}",
		"@ PRIVMSG", "@a", ": ", ":", "", " ", "@=;=; X", "PING :", "A " + strings.Repeat("b ", 40),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		command, err := Parse(line)
		if err != nil {
			return
		}
		if command.Name == "" || command.Name != strings.ToUpper(command.Name) {
			t.Fatalf("accepted command name %q from %q", command.Name, line)
		}
		for _, param := range command.Params {
			if strings.ContainsAny(param, "\r\n") {
				t.Fatalf("parameter kept a line break: %q", param)
			}
		}
		if strings.ContainsAny(command.Trailing, "\r\n") {
			t.Fatalf("trailing kept a line break: %q", command.Trailing)
		}
		_ = command.Text()
		_, _ = command.Param(0)
	})
}

func FuzzDecodeBody(f *testing.F) {
	for _, seed := range []string{"", "aGVsbG8", "!!!", EncodeBody("line one\nline two"), EncodeBody("\x00\xff")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, encoded string) {
		body, err := DecodeBody(encoded)
		if err != nil {
			return
		}
		if again, err := DecodeBody(EncodeBody(body)); err != nil || again != body {
			t.Fatalf("accepted body does not round-trip: %q %v", body, err)
		}
	})
}
