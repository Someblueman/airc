package config

import (
	"errors"
	"net"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// RedisStreams owns one fixed stream/group and stable consumer on an outbound
// broker. Namespace names the broker keyspace across endpoint migration.
type RedisStreams struct {
	ID            string           `json:"id"`
	Enabled       bool             `json:"enabled"`
	Namespace     string           `json:"namespace"`
	Address       string           `json:"address"`
	TLSServerName string           `json:"tls_server_name"`
	TLSCAFile     string           `json:"tls_ca_file,omitempty"`
	Credential    RedisCredential  `json:"credential"`
	Group         string           `json:"group"`
	Consumer      string           `json:"consumer"`
	Route         RedisStreamRoute `json:"route"`
}

// RedisStreamRoute posts each entry's plain request as the author it names,
// else as BoundAuthor. The Redis ACL on the stream is the access gate.
type RedisStreamRoute struct {
	BoundAuthor  *BoundAuthor     `json:"bound_author,omitempty"`
	Guests       *bool            `json:"guests,omitempty"`
	Stream       string           `json:"stream"`
	ResultStream string           `json:"result_stream"`
	Target       core.Target      `json:"target"`
	Operations   []core.Operation `json:"operations"`
}

func (c Config) validateRedisStreams() error {
	if len(c.RedisStreams) > 32 {
		return errors.New("redis stream owner count limit exceeded")
	}
	ids := map[string]bool{}
	namespaces, endpoints := map[string]string{}, map[string]string{}
	keys := map[string]map[string]bool{}
	for _, b := range c.RedisStreams {
		if !id(b.ID) || ids[b.ID] || !id(b.Namespace) || endpoint(b.Address, true) != nil {
			return errors.New("invalid or duplicate Redis stream owner")
		}
		ids[b.ID] = true
		host, port, _ := net.SplitHostPort(b.Address)
		ip := net.ParseIP(host)
		n, _ := strconv.Atoi(port)
		if ip.IsUnspecified() || n == 0 || !redisServerName(b.TLSServerName) || b.TLSCAFile != "" && !absolute(b.TLSCAFile) {
			return errors.New("redis streams requires verified TLS dial endpoint")
		}
		address := net.JoinHostPort(ip.String(), strconv.Itoa(n))
		if prior, ok := namespaces[b.Namespace]; ok && prior != address {
			return errors.New("redis namespace refers to multiple endpoints")
		}
		if prior, ok := endpoints[address]; ok && prior != b.Namespace {
			return errors.New("redis endpoint refers to multiple namespaces")
		}
		namespaces[b.Namespace], endpoints[address] = address, b.Namespace
		if !redisText(b.Credential.Username, 255) || !absolute(b.Credential.PasswordFile) || !RedisChannel(b.Group) || !RedisChannel(b.Consumer) {
			return errors.New("redis streams requires ACL credentials and fixed group/consumer")
		}
		r := b.Route
		if validRouteAuthor(r.BoundAuthor, r.Guests) != nil || !RedisChannel(r.Stream) || !RedisChannel(r.ResultStream) || r.Stream == r.ResultStream || len(r.Operations) == 0 || operations(r.Operations) != nil {
			return errors.New("redis streams requires fixed route and result stream")
		}
		if keys[b.Namespace] == nil {
			keys[b.Namespace] = map[string]bool{}
		}
		if keys[b.Namespace][r.Stream] || keys[b.Namespace][r.ResultStream] {
			return errors.New("redis stream roles collide")
		}
		keys[b.Namespace][r.Stream], keys[b.Namespace][r.ResultStream] = true, true
		for _, op := range r.Operations {
			if !redisOperation(op) || (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
				return errors.New("redis stream route has an unsupported operation or lacks a cursor key")
			}
		}
	}
	return nil
}
