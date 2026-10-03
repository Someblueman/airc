package server

import (
	"math/rand"
	"strings"
	"testing"
)

func TestSearchLowercaseSemantics(t *testing.T) {
	bodies := []string{"", "AbCdEfGh", strings.Repeat("Ab", 2048), strings.Repeat("A", 4096) + "B", "AaAaAB", "Kelvin K İ ı Σ σ ς ſ S ß", "🙂Café 東京", "prefix\xffEND", "[\\]^_` @?"}
	queries := []string{"", "abc", "FGH", "aaaaab", "ABCDend", strings.Repeat("a", 200) + "b", strings.Repeat("ab", 100) + "ac", "k", "i", "σ", "ς", "s", "ss", "café", "東京", "\xff", "[", "@"}
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("abAB[?kKiİσς🙂\u2028")
	for range 200 {
		var b strings.Builder
		for range rng.Intn(100) {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		bodies = append(bodies, b.String())
	}
	for _, body := range bodies {
		for _, query := range queries {
			lower := strings.ToLower(query)
			want := strings.Contains(strings.ToLower(body), lower)
			if got := newSearchText(lower).contains(body); got != want {
				t.Fatalf("body=%q query=%q: got %v want %v", body, query, got, want)
			}
		}
	}
}

func FuzzSearchText(f *testing.F) {
	for _, body := range []string{"AbCdEfGh", strings.Repeat("Ab", 200) + "C", "KİΣς🙂\xff", "@[]^_`"} {
		for offset := range 16 {
			f.Add(strings.Repeat("-", offset)+body, body)
			f.Add(strings.Repeat("-", offset)+body, "Not-found")
		}
	}
	f.Fuzz(func(t *testing.T, body, query string) {
		if len(body) > 4096 || len(query) > 256 {
			t.Skip()
		}
		want := strings.Contains(strings.ToLower(body), strings.ToLower(query))
		if got := newSearchText(query).contains(body); got != want {
			t.Fatalf("body=%q query=%q: got %v want %v", body, query, got, want)
		}
	})
}
