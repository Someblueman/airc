package config

import (
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// RedisPubSub describes an outbound broker, separate from inbound listeners.
type RedisPubSub struct {
	ID            string          `json:"id"`
	Enabled       bool            `json:"enabled"`
	Address       string          `json:"address"`
	TLSServerName string          `json:"tls_server_name"`
	TLSCAFile     string          `json:"tls_ca_file,omitempty"`
	Subscriber    RedisCredential `json:"subscriber"`
	Publisher     RedisCredential `json:"publisher"`
	Routes        []RedisRoute    `json:"routes"`
}

type RedisCredential struct {
	Username     string `json:"username"`
	PasswordFile string `json:"password_file"`
}

type RedisRoute struct {
	Principal       string           `json:"principal"`
	RequestChannel  string           `json:"request_channel"`
	ResponseChannel string           `json:"response_channel"`
	Target          core.Target      `json:"target"`
	Operations      []core.Operation `json:"operations"`
}

// RedisChannel accepts literal binary-safe UTF-8 names, including spaces and
// glob characters: subscriptions use SUBSCRIBE, never PSUBSCRIBE.
func RedisChannel(s string) bool {
	return redisText(s, 255)
}

func redisText(s string, max int) bool {
	if len(s) == 0 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func redisServerName(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func (c Config) validateRedisPubSub() error {
	if len(c.RedisPubSub) > 32 {
		return errors.New("redis broker count limit exceeded")
	}
	ids := make(map[string]bool)
	enabled := 0
	// Channels share a namespace when configurations dial the same endpoint.
	channels := make(map[string]map[string]bool)
	for _, b := range c.RedisPubSub {
		if !id(b.ID) || ids[b.ID] || endpoint(b.Address, true) != nil {
			return errors.New("invalid or duplicate Redis broker")
		}
		ids[b.ID] = true
		host, port, _ := net.SplitHostPort(b.Address)
		portNumber, _ := strconv.Atoi(port)
		ip := net.ParseIP(host)
		if ip.IsUnspecified() || portNumber == 0 || !redisServerName(b.TLSServerName) || b.TLSCAFile != "" && !absolute(b.TLSCAFile) {
			return errors.New("redis requires a dial endpoint and verified TLS server name")
		}
		for _, credential := range []RedisCredential{b.Subscriber, b.Publisher} {
			if !redisText(credential.Username, 255) || !absolute(credential.PasswordFile) {
				return errors.New("redis requires explicit ACL users and private password files")
			}
		}
		if len(b.Routes) < 1 || len(b.Routes) > 128 || c.ResponseSigningKey == nil {
			return errors.New("redis requires 1-128 fixed routes and a response signer")
		}
		address := net.JoinHostPort(ip.String(), strconv.Itoa(portNumber))
		if channels[address] == nil {
			channels[address] = make(map[string]bool)
		}
		seen := channels[address]
		for _, route := range b.Routes {
			if !id(route.Principal) || !RedisChannel(route.RequestChannel) || !RedisChannel(route.ResponseChannel) || route.RequestChannel == route.ResponseChannel || seen[route.RequestChannel] || seen[route.ResponseChannel] {
				return errors.New("invalid or duplicate Redis channel grant")
			}
			seen[route.RequestChannel], seen[route.ResponseChannel] = true, true
			if len(route.Operations) == 0 || operations(route.Operations) != nil {
				return errors.New("redis route requires distinct explicit operations")
			}
			authorized := false
			for _, p := range c.Principals {
				if p.ID != route.Principal || p.Revoked || len(p.SigningKeys) == 0 || !slices.Contains(p.Targets, route.Target) {
					continue
				}
				authorized = true
				for _, op := range route.Operations {
					if !redisOperation(op) || !slices.Contains(p.Operations, op) {
						return errors.New("redis route exceeds principal authority")
					}
					if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
						return errors.New("redis history/search requires a cursor key file")
					}
				}
				break
			}
			if !authorized {
				return errors.New("redis requires an active signing principal authorized for the exact target")
			}
		}
		if b.Enabled {
			enabled++
		}
	}
	if enabled*2 > c.Limits.Sessions {
		return errors.New("redis broker pairs exceed shared socket capacity")
	}
	return nil
}

func redisOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}
