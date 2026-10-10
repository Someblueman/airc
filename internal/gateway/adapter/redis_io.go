package adapter

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func redisCommand(args ...[]byte) rediswire.Value {
	v := rediswire.Value{Type: rediswire.Array, Array: make([]rediswire.Value, len(args))}
	for i, arg := range args {
		v.Array[i] = rediswire.Value{Type: rediswire.Bulk, Bytes: arg}
	}
	return v
}

func redisOK(v rediswire.Value) bool {
	return v.Type == rediswire.Simple && bytes.Equal(v.Bytes, []byte("OK"))
}

func redisWrite(c *tls.Conn, v rediswire.Value, deadline time.Time) error {
	wire, err := rediswire.Encode(v)
	if err != nil {
		return errRedisConnection
	}
	defer clear(wire) // AUTH wire includes the broker password.
	if !time.Now().Before(deadline) || c.SetWriteDeadline(deadline) != nil {
		return errRedisConnection
	}
	for len(wire) > 0 {
		n, err := c.Write(wire)
		if err != nil || n == 0 {
			return errRedisConnection
		}
		wire = wire[n:]
	}
	if !time.Now().Before(deadline) {
		return errRedisConnection
	}
	return nil
}

func redisRead(c *tls.Conn, r *bufio.Reader, deadline time.Time) (rediswire.Value, error) {
	if !time.Now().Before(deadline) || c.SetReadDeadline(deadline) != nil {
		return rediswire.Value{}, errRedisConnection
	}
	v, err := rediswire.Read(r)
	if err != nil || !time.Now().Before(deadline) {
		return rediswire.Value{}, errRedisConnection
	}
	return v, nil
}

func redisTriple(v rediswire.Value, kind string) bool {
	return v.Type == rediswire.Array && !v.Null && len(v.Array) == 3 && v.Array[0].Type == rediswire.Bulk && !v.Array[0].Null && bytes.Equal(v.Array[0].Bytes, []byte(kind)) && v.Array[1].Type == rediswire.Bulk && !v.Array[1].Null
}

func (p *redisPair) subscribe(routes []config.RedisRoute, deadline time.Time) error {
	// Pipeline bounded batches before reading. Interleaved publications during
	// establishment are discarded, never queued or treated as journal intake.
	for start := 0; start < len(routes); start += 64 {
		args := [][]byte{[]byte("SUBSCRIBE")}
		for _, r := range routes[start:min(start+64, len(routes))] {
			args = append(args, []byte(r.RequestChannel))
		}
		if redisWrite(p.subscriber, redisCommand(args...), deadline) != nil {
			return errRedisConnection
		}
	}
	confirmed := make(map[string]bool)
	for len(confirmed) < len(routes) {
		v, err := redisRead(p.subscriber, p.subReader, deadline)
		if err != nil {
			return err
		}
		if redisTriple(v, "message") && confirmed[string(v.Array[1].Bytes)] && v.Array[2].Type == rediswire.Bulk && !v.Array[2].Null {
			continue
		}
		if !redisTriple(v, "subscribe") || v.Array[2].Type != rediswire.Integer || v.Array[2].Integer != int64(len(confirmed)+1) {
			return errRedisConnection
		}
		channel := string(v.Array[1].Bytes)
		if _, ok := p.routes[channel]; !ok || confirmed[channel] {
			return errRedisConnection
		}
		confirmed[channel] = true
	}
	return nil
}

func (p *redisPair) next() (config.RedisRoute, []byte, error) {
	if p.ctx.Err() != nil || p.subscriber.SetReadDeadline(time.Time{}) != nil {
		p.close()
		return config.RedisRoute{}, nil, errRedisConnection
	}
	if _, err := p.subReader.Peek(1); err != nil {
		p.close()
		return config.RedisRoute{}, nil, errRedisConnection
	}
	v, err := redisRead(p.subscriber, p.subReader, time.Now().Add(redisDeadline))
	if err == nil && p.ctx.Err() == nil && redisTriple(v, "message") && v.Array[2].Type == rediswire.Bulk && !v.Array[2].Null {
		if route, ok := p.routes[string(v.Array[1].Bytes)]; ok {
			route.Operations = slices.Clone(route.Operations)
			return route, v.Array[2].Bytes, nil
		}
	}
	p.close()
	return config.RedisRoute{}, nil, errRedisConnection
}

// publish accepts a protocol count (including zero) without claiming durable or
// native receipt. Only a configured result channel may be used by the owner.
func (p *redisPair) publish(channel string, payload []byte) error {
	allowed := false
	for _, r := range p.routes {
		if r.ResponseChannel == channel {
			allowed = true
			break
		}
	}
	deadline := time.Now().Add(redisDeadline)
	if allowed && p.ctx.Err() == nil && redisWrite(p.publisher, redisCommand([]byte("PUBLISH"), []byte(channel), payload), deadline) == nil {
		v, err := redisRead(p.publisher, p.pubReader, deadline)
		if err == nil && p.ctx.Err() == nil && v.Type == rediswire.Integer && v.Integer >= 0 {
			return nil
		}
	}
	p.close()
	return errRedisConnection
}
