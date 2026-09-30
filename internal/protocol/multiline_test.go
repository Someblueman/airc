package protocol

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBodyRoundTripAndRejection(t *testing.T) {
	body := "line one\n\n  indented ; with \\ odd chars ✓\nlast"
	got, err := DecodeBody(EncodeBody(body))
	if err != nil || got != body {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	for name, bad := range map[string]string{
		"not base64": "!!!", "nul": EncodeBody("a\x00b"), "cr": EncodeBody("a\rb"), "invalid utf8": EncodeBody("a\xffb"),
	} {
		if _, err := DecodeBody(bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestNormalizeNewlines(t *testing.T) {
	if got := NormalizeNewlines("a\r\nb\rc\nd"); got != "a\nb\nc\nd" {
		t.Fatalf("got %q", got)
	}
}

func TestPreviewIsOneShortLine(t *testing.T) {
	if got := Preview("single line"); got != "single line" {
		t.Fatalf("single line changed: %q", got)
	}
	if got := Preview("first\n\n  second  \nthird"); got != "first ⏎ second ⏎ third" {
		t.Fatalf("preview = %q", got)
	}
	long := Preview(strings.Repeat("é", 500) + "\nend")
	if strings.ContainsAny(long, "\r\n") || !utf8.ValidString(long) || len(long) > PreviewLimit+len("…") || !strings.HasSuffix(long, "…") {
		t.Fatalf("long preview is not a bounded valid line (%d bytes)", len(long))
	}
}

func TestMaximumTaggedLineFitsLineLimit(t *testing.T) {
	body := strings.Repeat("x\n", 2048) // 4096 bytes, the largest body the server accepts
	line := "@msgid=" + strings.Repeat("a", 32) + ";time=2026-10-01T00:00:00.123456789Z;" + BodyTag + "=" + EncodeBody(body) +
		" :" + strings.Repeat("n", 30) + "!" + strings.Repeat("u", 32) + "@localhost PRIVMSG " + strings.Repeat("#", 64) + " :" + strings.Repeat("p", PreviewLimit+3)
	if len(line) > MaxLineLength {
		t.Fatalf("worst-case tagged line is %d bytes, over the %d limit", len(line), MaxLineLength)
	}
}
