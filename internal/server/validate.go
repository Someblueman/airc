package server

import "strings"

func validNick(nick string) bool {
	if len(nick) == 0 || len(nick) > 30 {
		return false
	}
	for i, r := range nick {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || strings.ContainsRune("[]\\`_^{|}", r)) {
				return false
			}
		} else if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("[]\\`_^{|}-", r)) {
			return false
		}
	}
	return true
}

func nickKey(nick string) string { return strings.ToLower(nick) }

func validChannel(channel string) bool {
	if len(channel) < 2 || len(channel) > 64 || (channel[0] != '#' && channel[0] != '&') {
		return false
	}
	for _, r := range channel {
		if r <= ' ' || r == ',' || r == ':' || r == 0x7f {
			return false
		}
	}
	return true
}
