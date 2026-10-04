package main

import "testing"

func TestAnyModifiedUpDownSelectsAMessage(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want []keyKind
	}{
		"plain arrows scroll":        {"\x1b[A\x1b[B", []keyKind{keyUp, keyDown}},
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
