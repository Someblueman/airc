package retention

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for text, want := range map[string]time.Duration{"0": 0, "30d": 720 * time.Hour, "72h": 72 * time.Hour, "1h": time.Hour} {
		if got, err := ParseAge(text); err != nil || got != want {
			t.Errorf("ParseAge(%q) = %v, %v", text, got, err)
		}
	}
	for _, bad := range []string{"", "30m", "d", "-1d", "soon", "1.5d"} {
		if _, err := ParseAge(bad); err == nil {
			t.Errorf("ParseAge(%q) accepted", bad)
		}
	}
	for text, want := range map[string]int64{"0": 0, "10GiB": 10 << 30, "64MiB": 64 << 20} {
		if got, err := ParseSize(text); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v", text, got, err)
		}
	}
	for _, bad := range []string{"1MiB", "lots"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) accepted", bad)
		}
	}
	if _, err := ParseAge(DefaultAge); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSize(DefaultSize); err != nil {
		t.Fatal(err)
	}
}
