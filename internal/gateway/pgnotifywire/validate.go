package pgnotifywire

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

var ErrProtocol = errors.New("postgres notification protocol failed")

func ValidateLocation(address, serverName, database, username, channel string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.Contains(host, "%") || !canonicalPort(port) {
		return ErrProtocol
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || strings.Contains(host, ":") && ip.To4() != nil || ip.String() != host || net.JoinHostPort(host, port) != address {
		return ErrProtocol
	}
	if !validTLSName(serverName) || !identifier(database) || !identifier(username) || !identifier(channel) {
		return ErrProtocol
	}
	return nil
}

func canonicalPort(value string) bool {
	if value == "" || len(value) > 5 || len(value) > 1 && value[0] == '0' {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(value)
	return err == nil && n >= 1 && n <= 65535
}

func validTLSName(name string) bool {
	if ip := net.ParseIP(name); ip != nil {
		return !ip.IsUnspecified() && !ip.IsMulticast() && !(strings.Contains(name, ":") && ip.To4() != nil) && ip.String() == name
	}
	if len(name) == 0 || len(name) > 253 || strings.ToLower(name) != name {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range label {
			b := label[i]
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
				return false
			}
		}
	}
	return true
}

func identifier(value string) bool {
	if len(value) == 0 || len(value) > 63 || !(value[0] == '_' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for i := 1; i < len(value); i++ {
		b := value[i]
		if !(b == '_' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9') {
			return false
		}
	}
	return true
}
