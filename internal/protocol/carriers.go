package protocol

import (
	"errors"
	"path"
	"strconv"
	"strings"
)

// Carrier is one advertised way for a client to reach the chat service: a
// carrier name ("irc" for aircd itself, otherwise an airc-gateway protocol such
// as "http" or "websocket"), whether it requires TLS, and either a port on the
// host the client already uses or a local Unix socket path. A list of carriers
// is in preference order: a client tries the first and falls back to the next.
type Carrier struct {
	Name string
	TLS  bool
	Port int
	Path string
}

// MaxCarriers bounds an advertised carrier list.
const MaxCarriers = 16

// String is the advertised form: NAME[+tls]:PORT or NAME:/socket/path.
func (c Carrier) String() string {
	name := c.Name
	if c.TLS {
		name += "+tls"
	}
	if c.Path != "" {
		return name + ":" + c.Path
	}
	return name + ":" + strconv.Itoa(c.Port)
}

// ParseCarrier parses one advertised carrier.
func ParseCarrier(s string) (Carrier, error) {
	name, endpoint, ok := strings.Cut(s, ":")
	if !ok {
		return Carrier{}, errors.New("carrier must be NAME:PORT or NAME:/path")
	}
	var c Carrier
	c.Name, c.TLS = strings.CutSuffix(name, "+tls")
	if !carrierName(c.Name) {
		return Carrier{}, errors.New("carrier name must be 1-32 lowercase letters, digits or underscores, starting with a letter")
	}
	if strings.HasPrefix(endpoint, "/") {
		if c.TLS || !carrierPath(endpoint) {
			return Carrier{}, errors.New("carrier socket path must be absolute, normalized, at most 107 bytes of printable ASCII without spaces, commas, semicolons, equals signs or backslashes, and without TLS")
		}
		c.Path = endpoint
		return c, nil
	}
	port, err := strconv.Atoi(endpoint)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != endpoint {
		return Carrier{}, errors.New("carrier port must be 1-65535")
	}
	c.Port = port
	return c, nil
}

// ParseCarriers parses a comma-separated carrier list; the empty string is an
// empty list. Entries must be distinct.
func ParseCarriers(list string) ([]Carrier, error) {
	if list == "" {
		return nil, nil
	}
	parts := strings.Split(list, ",")
	if len(parts) > MaxCarriers {
		return nil, errors.New("too many carriers")
	}
	carriers := make([]Carrier, 0, len(parts))
	seen := map[Carrier]bool{}
	for _, part := range parts {
		c, err := ParseCarrier(part)
		if err != nil {
			return nil, err
		}
		if seen[c] {
			return nil, errors.New("duplicate carrier " + part)
		}
		seen[c] = true
		carriers = append(carriers, c)
	}
	return carriers, nil
}

// FormatCarriers is the comma-separated advertised list, in order.
func FormatCarriers(carriers []Carrier) string {
	parts := make([]string, len(carriers))
	for i, c := range carriers {
		parts[i] = c.String()
	}
	return strings.Join(parts, ",")
}

func carrierName(s string) bool {
	if len(s) < 1 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func carrierPath(s string) bool {
	if len(s) > 107 || !path.IsAbs(s) || path.Clean(s) != s {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r > '~' || strings.ContainsRune(",;=\\", r) {
			return false
		}
	}
	return true
}
