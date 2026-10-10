package couchdbwire

import (
	"net"
	"strconv"
	"strings"
)

func validHost(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.Contains(host, ":") || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
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

func canonicalPort(port string) bool {
	if len(port) == 0 || len(port) > 5 || len(port) > 1 && port[0] == '0' {
		return false
	}
	for i := range port {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}
