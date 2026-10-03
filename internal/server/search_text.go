package server

import (
	"encoding/binary"
	"strings"
)

// ASCII searches avoid lowercase copies of every body. Unicode and unusually
// repetitive inputs use the existing standard-library search. A comparison
// budget bounds the fast path to linear work before that fallback.
type searchText struct {
	query   string
	unicode bool
}

func newSearchText(query string) searchText {
	q := searchText{query: strings.ToLower(query)}
	for i := range len(q.query) {
		if q.query[i] >= 128 {
			q.unicode = true
			break
		}
	}
	return q
}

func (q searchText) contains(body string) bool {
	query := q.query
	if query == "" {
		return true
	}
	if q.unicode {
		return strings.Contains(strings.ToLower(body), query)
	}
	comparisons := len(body) * 2
	const ones uint64 = 0x0101010101010101
	const high uint64 = 0x8080808080808080
	first := uint64(query[0]) * ones
	fold := uint64(0)
	if query[0] >= 'a' && query[0] <= 'z' {
		fold = 0x2020202020202020
	}
	for i := 0; i < len(body); i++ {
		// Skip eight ASCII bytes when none can start a match. OR only broadens
		// letter candidates; the scalar comparison below checks exact folding.
		// A zero byte in x is detected by (x-ones)&^x&high. Unsigned wrap is
		// intentional. Stop before non-ASCII so Unicode still uses ToLower.
		for len(body)-i >= 8 {
			word := binary.LittleEndian.Uint64([]byte(body[i : i+8]))
			x := (word | fold) ^ first
			if word&high != 0 || (x-ones)&^x&high != 0 {
				break
			}
			i += 8
		}
		if i == len(body) {
			break
		}
		c := body[i]
		if c >= 128 {
			return strings.Contains(strings.ToLower(body), query)
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != query[0] {
			continue
		}
		j := 1
		for j < len(query) && i+j < len(body) {
			comparisons--
			c = body[i+j]
			if c >= 128 || comparisons < 0 {
				return strings.Contains(strings.ToLower(body), query)
			}
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != query[j] {
				break
			}
			j++
		}
		if j == len(query) {
			return true
		}
	}
	return false
}
