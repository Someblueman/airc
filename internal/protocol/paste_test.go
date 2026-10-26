package protocol

import (
	"strings"
	"testing"
)

func TestPasteIDLinesAndText(t *testing.T) {
	content := []byte("one\ntwo\nthree")
	id := PasteID(content)
	if len(id) != 32 || !ValidMessageID(id) || id != PasteID([]byte("one\ntwo\nthree")) || id == PasteID([]byte("one\ntwo\nthree\n")) {
		t.Fatalf("id %q", id)
	}
	for text, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "\n\n": 2} {
		if got := PasteLines([]byte(text)); got != want {
			t.Errorf("PasteLines(%q) = %d, want %d", text, got, want)
		}
	}
	if !ValidPasteText([]byte("plain ✓ text\r\n")) || ValidPasteText([]byte("nul\x00")) || ValidPasteText([]byte("\xff\xfe")) {
		t.Error("text validity")
	}
}

func TestPasteNamesLanguagesAndSizes(t *testing.T) {
	for _, good := range []string{"a", "solver.go", "test output (run 2).log", strings.Repeat("x", MaxPasteNameBytes)} {
		if !ValidPasteName(good) {
			t.Errorf("%q should be a valid name", good)
		}
	}
	for _, bad := range []string{"", " lead", "trail ", "dir/file", `dir\file`, "tab\tname", "new\nline", strings.Repeat("x", MaxPasteNameBytes+1), "\xff"} {
		if ValidPasteName(bad) {
			t.Errorf("%q should not be a valid name", bad)
		}
	}
	if !ValidPasteLanguage("") || !ValidPasteLanguage("c++") || !ValidPasteLanguage("objective-c") || ValidPasteLanguage("go lang") || ValidPasteLanguage(strings.Repeat("g", 31)) {
		t.Error("language validity")
	}
	for size, want := range map[int]string{0: "0 B", 1023: "1023 B", 1024: "1 KB", 1536: "1.5 KB", 12 * 1024: "12 KB", 20480: "20 KB", 3 << 20: "3 MB"} {
		if got := FormatPasteSize(size); got != want {
			t.Errorf("FormatPasteSize(%d) = %q, want %q", size, got, want)
		}
	}
}

func TestPasteReferenceLineAndCaption(t *testing.T) {
	p := PasteInfo{ID: strings.Repeat("ab", 16), Name: "go.diff", Language: "diff", Size: 20480, Lines: 300}
	line := PasteReferenceLine(p)
	if line != "paste:"+p.ID+" go.diff (20 KB, 300 lines)" {
		t.Fatalf("reference line %q", line)
	}
	if got := PasteMessageBody("", p); got != line {
		t.Fatalf("body without caption %q", got)
	}
	body := PasteMessageBody("Please review\r\nthe loop\n", p)
	if body != "Please review\nthe loop\n"+line {
		t.Fatalf("body %q", body)
	}
	if got := PasteCaption(body, p); got != "Please review\nthe loop" {
		t.Fatalf("caption %q", got)
	}
	if got := PasteCaption(line, p); got != "" {
		t.Fatalf("caption of a bare reference %q", got)
	}
	if one := PasteReferenceLine(PasteInfo{ID: p.ID, Size: 3, Lines: 1}); one != "paste:"+p.ID+" paste (3 B, 1 line)" {
		t.Fatalf("one-line unnamed paste %q", one)
	}
}
