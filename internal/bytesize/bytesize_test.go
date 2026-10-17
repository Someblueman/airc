package bytesize

import "testing"

func TestParseAndFormat(t *testing.T) {
	for text, want := range map[string]int64{
		"0": 0, "4096": 4096, "1KiB": 1024, "1k": 1024, "1.5GiB": 3 << 29, "1GB": 1e9,
		"512MiB": 512 << 20, " 2 gib ": 2 << 30, "10TB": 10e12, "64b": 64,
	} {
		if got, err := Parse(text); err != nil || got != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	for _, bad := range []string{"", "GiB", "-1GiB", "1XB", "lots", "1e30TiB", "NaN"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
	for bytes, want := range map[int64]string{1 << 30: "1GiB", 1536 << 20: "1536MiB", 1000: "1000", 3 << 40: "3TiB"} {
		if got := Format(bytes); got != want {
			t.Errorf("Format(%d) = %q, want %q", bytes, got, want)
		}
		if parsed, err := Parse(Format(bytes)); err != nil || parsed != bytes {
			t.Errorf("Format(%d) does not round-trip", bytes)
		}
	}
}
