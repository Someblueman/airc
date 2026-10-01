package main

import (
	"unicode"
	"unicode/utf8"
)

type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyBackspace
	keyDelete
	keyTab
	keyBackTab
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPgUp
	keyPgDn
	keyEsc
	keyCtrl // r holds the letter: 'c' for Ctrl-C
)

type key struct {
	kind keyKind
	r    rune
}

// parseKeys decodes one read from a raw terminal into key presses. A read holds
// whole escape sequences, so a lone ESC byte really is the Escape key.
func parseKeys(data []byte) []key {
	var keys []key
	for i := 0; i < len(data); {
		b := data[i]
		switch {
		case b == 0x1b:
			if i+1 >= len(data) {
				keys = append(keys, key{kind: keyEsc})
				i++
				continue
			}
			if data[i+1] == '[' || data[i+1] == 'O' {
				k, n := parseEscape(data[i:])
				if k.kind != keyRune || k.r != 0 {
					keys = append(keys, k)
				}
				i += n
				continue
			}
			keys = append(keys, key{kind: keyEsc})
			i++
		case b == '\r' || b == '\n':
			keys = append(keys, key{kind: keyEnter})
			i++
		case b == 0x7f || b == 0x08:
			keys = append(keys, key{kind: keyBackspace})
			i++
		case b == '\t':
			keys = append(keys, key{kind: keyTab})
			i++
		case b >= 0x01 && b <= 0x1a:
			keys = append(keys, key{kind: keyCtrl, r: rune('a' + b - 1)})
			i++
		default:
			r, size := utf8.DecodeRune(data[i:])
			if r != utf8.RuneError && unicode.IsPrint(r) {
				keys = append(keys, key{kind: keyRune, r: r})
			}
			i += size
		}
	}
	return keys
}

// parseEscape decodes an ESC [ or ESC O sequence and reports how many bytes it
// used. An unrecognised sequence yields a zero key, which the caller discards.
func parseEscape(data []byte) (key, int) {
	i := 2
	for i < len(data) && (data[i] < 0x40 || data[i] > 0x7e) {
		i++ // parameter and intermediate bytes
	}
	if i >= len(data) {
		return key{}, len(data)
	}
	params, final := string(data[2:i]), data[i]
	n := i + 1
	switch final {
	case 'A':
		return key{kind: keyUp}, n
	case 'B':
		return key{kind: keyDown}, n
	case 'C':
		return key{kind: keyRight}, n
	case 'D':
		return key{kind: keyLeft}, n
	case 'H':
		return key{kind: keyHome}, n
	case 'F':
		return key{kind: keyEnd}, n
	case 'Z':
		return key{kind: keyBackTab}, n
	case '~':
		switch params {
		case "1", "7":
			return key{kind: keyHome}, n
		case "4", "8":
			return key{kind: keyEnd}, n
		case "3":
			return key{kind: keyDelete}, n
		case "5":
			return key{kind: keyPgUp}, n
		case "6":
			return key{kind: keyPgDn}, n
		}
	}
	return key{}, n
}
