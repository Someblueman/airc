package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestTerminalReadBoundaries(t *testing.T) {
	input := []byte("é🐈\x1b[1;5A\x1b[27;2;13~\x1b\r\x1bf\x1b[200~one\r\ntwo\t猫\n\x03\x1b[201~\r")
	whole := new(keyDecoder).feed(input)
	for split := 0; split <= len(input); split++ {
		var d keyDecoder
		got := d.feed(input[:split])
		got = append(got, d.feed(input[split:])...)
		if !reflect.DeepEqual(got, whole) {
			t.Fatalf("split %d: %v != %v", split, got, whole)
		}
	}
	var d keyDecoder
	var got []key
	for _, b := range input {
		got = append(got, d.feed([]byte{b})...)
	}
	if !reflect.DeepEqual(got, whole) {
		t.Fatal("single-byte reads changed keys")
	}
	text := ""
	sends := 0
	for _, k := range got {
		if k.kind == keyPasteRune {
			text += string(k.r)
		}
		if k.kind == keyEnter {
			sends++
		}
		if k.kind == keyCtrl {
			t.Fatal("paste executed a control key")
		}
	}
	if text != "one\ntwo\t猫\n" || sends != 1 {
		t.Fatalf("text=%q submits=%d", text, sends)
	}
}

func TestPasteDraftAndLocalRejection(t *testing.T) {
	m := newTestModel("#room")
	m.connected = true
	var d keyDecoder
	for _, k := range d.feed([]byte("\x1b[200~line one\nline two é🐈\x1b[201~")) {
		cmds, quit := m.update(keyIn(k))
		if len(cmds) != 0 || quit {
			t.Fatal("paste triggered an action", cmds, quit)
		}
	}
	draft := string(m.input)
	if draft != "line one\nline two é🐈" {
		t.Fatal(draft)
	}
	row, _ := m.inputRow(100)
	if strings.ContainsAny(row, "\r\n\t") {
		t.Fatal("draft escaped its input row", row)
	}
	m.switchTo("@me")
	if len(m.input) != 0 {
		t.Fatal("room draft leaked into another view")
	}
	m.input, m.cursor = []rune(draft), len([]rune(draft))
	if cmds, _ := m.update(keyIn{kind: keyEnter}); len(cmds) != 0 || string(m.input) != draft {
		t.Fatal("read-only rejection lost draft")
	}
	m.switchTo("#room")
	cmds, _ := m.update(keyIn{kind: keyEnter})
	if len(cmds) != 1 || cmds[0].text != draft || string(m.input) != draft {
		t.Fatal("draft cleared before acknowledgement", cmds)
	}
	// A full queue returns the same command without ever attempting a network send.
	m.update(deliveryIn{cmd: cmds[0], err: errors.New("busy")})
	if m.pending != nil || string(m.input) != draft {
		t.Fatal("busy queue lost draft")
	}
	cmds, _ = m.update(keyIn{kind: keyEnter})
	m.update(keyIn{kind: keyRune, r: 'x'})
	if string(m.input) != draft {
		t.Fatal("in-flight draft changed")
	}
	m.update(deliveryIn{cmd: cmds[0]})
	if len(m.input) != 0 {
		t.Fatal("accepted draft not cleared")
	}
}

func FuzzBracketedPasteBoundaries(f *testing.F) {
	for _, s := range []string{"hello", "é🐈\nline two", "\r\n\t猫", "\x03/quit"} {
		f.Add(s, uint8(1))
	}
	f.Fuzz(func(t *testing.T, text string, chunk uint8) {
		if len(text) > 4096 || !utf8.ValidString(text) {
			t.Skip()
		}
		// ESC belongs to terminal framing; all other pasted controls are inert.
		text = strings.ReplaceAll(text, "\x1b", "")
		wire := []byte("\x1b[200~" + text + "\x1b[201~")
		var d keyDecoder
		var got strings.Builder
		for i := 0; i < len(wire); {
			end := min(len(wire), i+1+int(chunk))
			for _, k := range d.feed(wire[i:end]) {
				if k.kind != keyPasteRune {
					t.Fatalf("paste emitted action %v", k)
				}
				got.WriteRune(k.r)
			}
			i = end
		}
		want := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		want = strings.Map(func(r rune) rune {
			if unicode.IsPrint(r) || r == '\n' || r == '\t' {
				return r
			}
			return -1
		}, want)
		if got.String() != want || len(d.pending) != 0 || d.paste {
			t.Fatalf("got %q want %q", got.String(), want)
		}
	})
}

func TestAnyModifiedUpDownSelectsAMessage(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want []keyKind
	}{
		"shift":                      {"\x1b[1;2A\x1b[1;2B", []keyKind{keySelectPrev, keySelectNext}},
		"alt or option":              {"\x1b[1;3A\x1b[1;3B", []keyKind{keySelectPrev, keySelectNext}},
		"ctrl":                       {"\x1b[1;5A\x1b[1;5B", []keyKind{keySelectPrev, keySelectNext}},
		"ctrl shift":                 {"\x1b[1;6A", []keyKind{keySelectPrev}},
		"option sent as meta prefix": {"\x1b\x1b[A\x1b\x1bOB", []keyKind{keySelectPrev, keySelectNext}},
		"meta prefix on other keys":  {"\x1b\x1b[C", []keyKind{keyEsc, keyRight}},
	} {
		var d keyDecoder
		got := d.feed([]byte(tc.in))
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %d keys %v, want %v", name, len(got), got, tc.want)
			continue
		}
		for i, k := range got {
			if k.kind != tc.want[i] {
				t.Errorf("%s: key %d = %v, want %v", name, i, k.kind, tc.want[i])
			}
		}
	}

	// A meta-prefixed arrow split across reads decodes once, with no stray Esc.
	var d keyDecoder
	if got := d.feed([]byte("\x1b\x1b[")); len(got) != 0 {
		t.Fatalf("incomplete sequence produced %v", got)
	}
	if got := d.feed([]byte("A")); len(got) != 1 || got[0].kind != keySelectPrev {
		t.Fatalf("completed sequence decoded as %v, want select previous", got)
	}
}
