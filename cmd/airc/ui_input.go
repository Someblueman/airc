package main

import (
	"bytes"
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
	keySelectPrev
	keySelectNext
	keyPasteRune
	keyCtrl // r holds the letter: 'c' for Ctrl-C
)

type key struct {
	kind keyKind
	r    rune
}

// keyDecoder retains incomplete terminal sequences between reads. Bracketed
// paste is text, including newlines; it can never produce submission keys.
type keyDecoder struct {
	pending []byte
	paste   bool
	pasteCR bool
}

func (d *keyDecoder) feed(data []byte) []key {
	d.pending = append(d.pending, data...)
	var keys []key
	for len(d.pending) > 0 {
		data := d.pending
		if d.paste {
			end := []byte("\x1b[201~")
			if bytes.HasPrefix(data, end) {
				d.paste = false
				d.pasteCR = false
				d.pending = data[len(end):]
				continue
			}
			if bytes.HasPrefix(end, data) {
				break
			}
			if !utf8.FullRune(data) {
				break
			}
			r, n := utf8.DecodeRune(data)
			d.pending = data[n:]
			if r == '\n' && d.pasteCR {
				d.pasteCR = false
				continue
			}
			d.pasteCR = r == '\r'
			if r == '\r' {
				r = '\n'
			}
			if unicode.IsPrint(r) || r == '\n' || r == '\t' {
				keys = append(keys, key{keyPasteRune, r})
			}
			continue
		}
		b := data[0]
		n := 1
		switch {
		case b == 0x1b:
			if len(data) == 1 {
				return keys
			}
			if data[1] == '[' || data[1] == 'O' {
				k, used := parseEscape(data)
				if used == 0 {
					if len(data) > 64 {
						d.pending = nil
					}
					return keys
				}
				n = used
				if string(data[:n]) == "\x1b[200~" {
					d.paste = true
				} else if k.kind != keyRune || k.r != 0 {
					keys = append(keys, k)
				}
			} else {
				keys = append(keys, key{kind: keyEsc})
			}
		case b == '\r' || b == '\n':
			keys = append(keys, key{kind: keyEnter})
		case b == 0x7f || b == 0x08:
			keys = append(keys, key{kind: keyBackspace})
		case b == '\t':
			keys = append(keys, key{kind: keyTab})
		case b >= 1 && b <= 26:
			keys = append(keys, key{keyCtrl, rune('a' + b - 1)})
		default:
			if !utf8.FullRune(data) {
				return keys
			}
			r, size := utf8.DecodeRune(data)
			n = size
			if unicode.IsPrint(r) {
				keys = append(keys, key{keyRune, r})
			}
		}
		d.pending = data[n:]
	}
	return keys
}

// A bare Escape key is ambiguous until the terminal sequence timeout expires.
func (d *keyDecoder) idle() []key {
	if !d.paste && bytes.Equal(d.pending, []byte{0x1b}) {
		d.pending = nil
		return []key{{kind: keyEsc}}
	}
	return nil
}

// parseEscape decodes an ESC [ or ESC O sequence and reports how many bytes it
// used. An unrecognised sequence yields a zero key, which the caller discards.
func parseEscape(data []byte) (key, int) {
	i := 2
	for i < len(data) && (data[i] < 0x40 || data[i] > 0x7e) {
		i++ // parameter and intermediate bytes
	}
	if i >= len(data) {
		return key{}, 0
	}
	params, final := string(data[2:i]), data[i]
	n := i + 1
	switch final {
	case 'A':
		if params == "1;5" {
			return key{kind: keySelectPrev}, n
		}
		return key{kind: keyUp}, n
	case 'B':
		if params == "1;5" {
			return key{kind: keySelectNext}, n
		}
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
