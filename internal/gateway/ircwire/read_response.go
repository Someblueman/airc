package ircwire

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ResponseMarker starts each NOTICE line of a bridged read result.
const ResponseMarker = "AIRC-RESULT/1"

// ResponsePartBytes keeps each line well inside IRC's 512-byte limit after
// the server prefixes the bridge's own source.
const ResponsePartBytes = 360

// MaxResponseLines bounds one result.
const MaxResponseLines = 64

// PlainResponseLines splits text (valid UTF-8 without CR, LF or NUL, such as
// encoded JSON) into NOTICE lines to nickname, each
// "AIRC-RESULT/1 <id> <index>/<count> <part>", split on character boundaries.
// Joining the parts in index order restores text.
func PlainResponseLines(nickname, id, text string) ([][]byte, error) {
	if !responseNickname(nickname) || !responseID(id) || text == "" || !utf8.ValidString(text) || strings.ContainsAny(text, "\r\n\x00") {
		return nil, ErrLine
	}
	var parts []string
	for rest := text; rest != ""; {
		n := min(len(rest), ResponsePartBytes)
		for n < len(rest) && n > 0 && !utf8.RuneStart(rest[n]) {
			n--
		}
		parts = append(parts, rest[:n])
		rest = rest[n:]
	}
	if len(parts) > MaxResponseLines {
		return nil, ErrLine
	}
	lines := make([][]byte, 0, len(parts))
	for i, part := range parts {
		body := ResponseMarker + " " + id + " " + strconv.Itoa(i) + "/" + strconv.Itoa(len(parts)) + " " + part
		line, err := Encode("NOTICE", []string{nickname}, &body)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func responseID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func responseNickname(nick string) bool {
	if len(nick) == 0 || len(nick) > 30 || nick[0] >= '0' && nick[0] <= '9' || nick[0] == '-' {
		return false
	}
	for _, c := range nick {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-[]\\`^{}|_", c)) {
			return false
		}
	}
	return true
}
