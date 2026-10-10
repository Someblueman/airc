package config

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidateOrigins accepts only exact browser-serialized HTTP(S) origins.
// Plain HTTP is restricted to loopback development origins. No wildcard,
// opaque origin, path, credentials or normalization at request time is allowed.
func ValidateOrigins(origins []string) error {
	if len(origins) > 16 {
		return errors.New("too many allowed origins")
	}
	seen := map[string]bool{}
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || len(origin) > 512 || seen[origin] || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || origin != u.Scheme+"://"+u.Host || u.Host != strings.ToLower(u.Host) {
			return errors.New("invalid or duplicate allowed origin")
		}
		host := u.Hostname()
		if host == "" {
			return errors.New("empty origin host")
		}
		for _, c := range host {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == ':') {
				return errors.New("origin host must be canonical ASCII")
			}
		}
		ip := net.ParseIP(host)
		numeric := true
		for _, part := range strings.Split(host, ".") {
			if _, err := strconv.ParseUint(part, 0, 32); err != nil {
				if _, err := strconv.ParseUint(part, 10, 32); err != nil {
					numeric = false
				}
			}
		}
		if numeric && (ip == nil || ip.String() != host) {
			return errors.New("noncanonical numeric origin host")
		}

		if strings.Contains(host, ":") && (ip == nil || originIPv6(ip) != host) {
			return errors.New("invalid origin IP")
		}
		if u.Scheme == "http" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return errors.New("plaintext origin must be loopback")
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || u.Scheme == "https" && n == 443 || u.Scheme == "http" && n == 80 {
				return errors.New("invalid or noncanonical origin port")
			}
		} else if strings.HasSuffix(u.Host, ":") {
			return errors.New("empty origin port")
		}
		seen[origin] = true
	}
	return nil
}

// Browser IPv6 serialization uses hex groups even for IPv4-mapped addresses.
func originIPv6(ip net.IP) string {
	bytes := ip.To16()
	groups := make([]string, 8)
	bestStart, bestLength := -1, 1
	for i := 0; i < 8; {
		value := uint16(bytes[2*i])<<8 | uint16(bytes[2*i+1])
		groups[i] = strconv.FormatUint(uint64(value), 16)
		i++
	}
	for i := 0; i < 8; {
		if groups[i] != "0" {
			i++
			continue
		}
		start := i
		for i < 8 && groups[i] == "0" {
			i++
		}
		if i-start > bestLength {
			bestStart, bestLength = start, i-start
		}
	}
	if bestStart < 0 {
		return strings.Join(groups, ":")
	}
	return strings.Join(groups[:bestStart], ":") + "::" + strings.Join(groups[bestStart+bestLength:], ":")
}
