// Package matrixwire contains bounded parsed Matrix data, not identity proofs.
package matrixwire

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// UserID accepts current Matrix localparts and DNS/IP server names with an
// optional numeric port. Values are compared exactly, never normalized.
func UserID(s string) bool {
	if len(s) < 4 || len(s) > 255 || s[0] != '@' {
		return false
	}
	local, server, ok := strings.Cut(s[1:], ":")
	if !ok || local == "" || !serverName(server) {
		return false
	}
	for _, c := range local {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("._=-/+", c)) {
			return false
		}
	}
	return true
}

// RoomID and EventID retain opaque case-sensitive legacy and domainless IDs.
// The selected gateway profile refuses controls and whitespace, bounds IDs to
// 255 bytes, and never infers a homeserver or authorization from their content.
func RoomID(s string) bool  { return opaqueID(s, '!') }
func EventID(s string) bool { return opaqueID(s, '$') }
func opaqueID(s string, sigil byte) bool {
	if len(s) < 2 || len(s) > 255 || s[0] != sigil || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s[1:] {
		if unicode.IsControl(c) || unicode.IsSpace(c) {
			return false
		}
	}
	return true
}

func serverName(s string) bool {
	if s == "" {
		return false
	}
	host := s
	if strings.HasPrefix(s, "[") || strings.Contains(s, ":") {
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			host = s[1 : len(s)-1]
		} else {
			var port string
			var err error
			host, port, err = net.SplitHostPort(s)
			if err != nil || port == "" || len(port) > 5 {
				return false
			}
			for _, c := range port {
				if c < '0' || c > '9' {
					return false
				}
			}
			n, err := strconv.ParseUint(port, 10, 16)
			if err != nil || n == 0 {
				return false
			}
		}
		if strings.HasPrefix(s, "[") {
			ip, err := netip.ParseAddr(host)
			return err == nil && ip.Is6() && ip.Zone() == ""
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Is4()
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
