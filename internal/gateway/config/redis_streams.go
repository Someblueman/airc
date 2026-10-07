package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"slices"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// RedisStreams owns one fixed stream/group and stable consumer on an outbound
// broker. Namespace survives endpoint migration and is part of signed identity.
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

type RedisStreamRoute struct {
	Principal    string           `json:"principal"`
	Stream       string           `json:"stream"`
	ResultStream string           `json:"result_stream"`
	Target       core.Target      `json:"target"`
	Operations   []core.Operation `json:"operations"`
}

// RedisStreamRequestID binds signed intent to an immutable entry identity. Realm,
// principal and binding remain in the existing journal identity scope.
func RedisStreamRequestID(namespace, stream, entry string) (string, error) {
	if !id(namespace) || !RedisChannel(stream) || !rediswire.StreamEntryID(entry) {
		return "", errors.New("invalid Redis stream identity")
	}
	h := sha256.Sum256([]byte("redis-stream-request\x00" + namespace + "\x00" + stream + "\x00" + entry))
	return hex.EncodeToString(h[:]), nil
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
		if !id(r.Principal) || !RedisChannel(r.Stream) || !RedisChannel(r.ResultStream) || r.Stream == r.ResultStream || len(r.Operations) == 0 || operations(r.Operations) != nil || c.ResponseSigningKey == nil {
			return errors.New("redis streams requires fixed signed route and result stream")
		}
		if keys[b.Namespace] == nil {
			keys[b.Namespace] = map[string]bool{}
		}
		if keys[b.Namespace][r.Stream] || keys[b.Namespace][r.ResultStream] {
			return errors.New("redis stream roles collide")
		}
		keys[b.Namespace][r.Stream], keys[b.Namespace][r.ResultStream] = true, true
		authorized := false
		for _, p := range c.Principals {
			if p.ID != r.Principal || p.Revoked || len(p.SigningKeys) == 0 || !slices.Contains(p.Targets, r.Target) {
				continue
			}
			authorized = true
			for _, op := range r.Operations {
				if !redisOperation(op) || !slices.Contains(p.Operations, op) || (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("redis stream route exceeds signed authority or lacks cursor key")
				}
			}
			break
		}
		if !authorized {
			return errors.New("redis stream route requires active signing principal")
		}
		if b.Enabled {
			return errors.New("redis streams service integration is not enabled")
		}
	}
	return nil
}
