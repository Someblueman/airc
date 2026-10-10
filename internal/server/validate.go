package server

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func nickKey(nick string) string { return strings.ToLower(nick) }

func validChannel(channel string) bool {
	if len(channel) < 2 || len(channel) > 64 || !utf8.ValidString(channel) || (channel[0] != '#' && channel[0] != '&') {
		return false
	}
	for _, r := range channel {
		if unicode.IsControl(r) || r <= ' ' || r == ',' || r == ':' {
			return false
		}
	}
	return true
}
