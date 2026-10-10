package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// redisStreamConn has one serial command owner. Cancellation closes the raw
// socket and close joins that callback before releasing shared capacity.
type redisStreamConn struct {
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool
	closed chan struct{}
	once   sync.Once
	mu     sync.Mutex
	raw    net.Conn
	slots  chan struct{}
	conn   *tls.Conn
	reader *bufio.Reader
	broker config.RedisStreams
}

func openRedisStream(parent context.Context, b config.RedisStreams, security *tls.Config, password []byte, slots chan struct{}) (*redisStreamConn, error) {
	if !redisStreamInputs(b, security, password, slots) {
		return nil, errRedisConnection
	}
	select {
	case slots <- struct{}{}:
	default:
		return nil, errRedisConnection
	}
	ctx, cancel := context.WithCancel(parent)
	p := &redisStreamConn{ctx: ctx, cancel: cancel, closed: make(chan struct{}), slots: slots, broker: b}
	p.stop = context.AfterFunc(ctx, func() { p.closeSocket(); close(p.closed) })
	setup, done := context.WithTimeout(ctx, redisDeadline)
	defer done()
	deadline, _ := setup.Deadline()
	raw, err := (&net.Dialer{}).DialContext(setup, "tcp", b.Address)
	if err == nil {
		p.mu.Lock()
		if ctx.Err() != nil {
			_ = raw.Close()
			err = errRedisConnection
		} else {
			p.raw = raw
		}
		p.mu.Unlock()
	}
	if err == nil {
		security = security.Clone()
		security.MinVersion = max(security.MinVersion, tls.VersionTLS13)
		p.conn = tls.Client(raw, security)
		p.reader = bufio.NewReader(p.conn)
		if p.conn.SetDeadline(deadline) != nil || p.conn.HandshakeContext(setup) != nil || redisWrite(p.conn, redisCommand([]byte("AUTH"), []byte(b.Credential.Username), password), deadline) != nil {
			err = errRedisConnection
		} else {
			v, readErr := redisRead(p.conn, p.reader, deadline)
			if readErr != nil || !redisOK(v) {
				err = errRedisConnection
			}
		}
	}
	if err != nil || ctx.Err() != nil {
		p.close()
		return nil, errRedisConnection
	}
	return p, nil
}

func (p *redisStreamConn) closeSocket() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.raw != nil {
		_ = p.raw.Close()
	}
}

func (p *redisStreamConn) close() {
	p.once.Do(func() {
		p.cancel()
		if !p.stop() {
			<-p.closed
		}
		p.closeSocket()
		<-p.slots
	})
}

// next reads one new entry or scans this stable consumer's pending entries after
// a canonical cursor. The group must already exist; ownership is never claimed
// from another consumer. A bounded idle read returns nil.
func (p *redisStreamConn) next(cursor string) (*rediswire.StreamEntry, error) {
	if cursor != ">" && cursor != "0-0" && !rediswire.StreamEntryID(cursor) {
		p.close()
		return nil, errRedisConnection
	}
	args := [][]byte{[]byte("XREADGROUP"), []byte("GROUP"), []byte(p.broker.Group), []byte(p.broker.Consumer), []byte("COUNT"), []byte("1")}
	if cursor == ">" {
		args = append(args, []byte("BLOCK"), []byte("1000"))
	}
	args = append(args, []byte("STREAMS"), []byte(p.broker.Route.Stream), []byte(cursor))
	deadline := time.Now().Add(redisDeadline)
	if p.ctx.Err() == nil && redisWrite(p.conn, redisCommand(args...), deadline) == nil && p.conn.SetReadDeadline(deadline) == nil {
		entry, err := rediswire.ReadStreamEntry(p.reader, p.broker.Route.Stream)
		if err == nil && p.ctx.Err() == nil && time.Now().Before(deadline) {
			return entry, nil
		}
	}
	p.close()
	return nil, errRedisConnection
}

// publish bounds the result stream to 1024 entries with exact trimming. Reply
// loss remains uncertain: retry can append a duplicate result.
func (p *redisStreamConn) publish(payload []byte) error {
	if len(payload) < 1 || len(payload) > rediswire.MaxBulk {
		p.close()
		return errRedisConnection
	}
	v, err := p.command(redisCommand([]byte("XADD"), []byte(p.broker.Route.ResultStream), []byte("MAXLEN"), []byte("="), []byte("1024"), []byte("*"), []byte("result"), payload))
	if err == nil && v.Type == rediswire.Bulk && !v.Null && rediswire.StreamEntryID(string(v.Bytes)) {
		return nil
	}
	p.close()
	return errRedisConnection
}

// ack accepts zero for an entry already removed from the pending list. This is
// broker bookkeeping, never evidence of native AIRC acceptance.
func (p *redisStreamConn) ack(entryID string) error {
	if !rediswire.StreamEntryID(entryID) {
		p.close()
		return errRedisConnection
	}
	v, err := p.command(redisCommand([]byte("XACK"), []byte(p.broker.Route.Stream), []byte(p.broker.Group), []byte(entryID)))
	if err == nil && v.Type == rediswire.Integer && (v.Integer == 0 || v.Integer == 1) {
		return nil
	}
	p.close()
	return errRedisConnection
}

func (p *redisStreamConn) command(command rediswire.Value) (rediswire.Value, error) {
	deadline := time.Now().Add(redisDeadline)
	if p.ctx.Err() == nil && redisWrite(p.conn, command, deadline) == nil {
		v, err := redisRead(p.conn, p.reader, deadline)
		if err == nil && p.ctx.Err() == nil {
			return v, nil
		}
	}
	p.close()
	return rediswire.Value{}, errRedisConnection
}

func redisStreamInputs(b config.RedisStreams, security *tls.Config, password []byte, slots chan struct{}) bool {
	if security == nil || security.InsecureSkipVerify || security.ServerName == "" || security.ServerName != b.TLSServerName || !config.RedisChannel(b.Credential.Username) || !config.RedisChannel(b.Group) || !config.RedisChannel(b.Consumer) || !config.RedisChannel(b.Route.Stream) || !config.RedisChannel(b.Route.ResultStream) || b.Route.Stream == b.Route.ResultStream || len(password) < 1 || len(password) > auth.MaxRedisPassword || cap(slots) < 1 || cap(slots) > 256 {
		return false
	}
	return true
}
