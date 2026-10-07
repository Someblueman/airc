package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
)

const redisDeadline = 3 * time.Second

var errRedisConnection = errors.New("redis connection refused")

// redisPair has one application owner. Cancellation only closes raw sockets;
// close joins that callback before returning shared capacity to the service.
type redisPair struct {
	ctx                   context.Context
	cancel                context.CancelFunc
	stop                  func() bool
	closed                chan struct{}
	once                  sync.Once
	mu                    sync.Mutex
	raw                   []net.Conn
	slots                 chan struct{}
	subscriber, publisher *tls.Conn
	subReader, pubReader  *bufio.Reader
	routes                map[string]config.RedisRoute
}

func openRedisPair(parent context.Context, b config.RedisPubSub, security *tls.Config, subscriberPassword, publisherPassword []byte, slots chan struct{}) (*redisPair, error) {
	if security == nil || security.InsecureSkipVerify || security.ServerName != b.TLSServerName || security.ServerName == "" || !config.RedisChannel(b.Subscriber.Username) || !config.RedisChannel(b.Publisher.Username) || len(b.Routes) < 1 || len(b.Routes) > 128 || len(subscriberPassword) < 1 || len(subscriberPassword) > auth.MaxRedisPassword || len(publisherPassword) < 1 || len(publisherPassword) > auth.MaxRedisPassword || cap(slots) < 2 || cap(slots) > 256 {
		return nil, errRedisConnection
	}
	routes := make(map[string]config.RedisRoute, len(b.Routes))
	channels := make(map[string]bool)
	for _, r := range b.Routes {
		if !config.RedisChannel(r.RequestChannel) || !config.RedisChannel(r.ResponseChannel) || r.RequestChannel == r.ResponseChannel || channels[r.RequestChannel] || channels[r.ResponseChannel] {
			return nil, errRedisConnection
		}
		channels[r.RequestChannel], channels[r.ResponseChannel] = true, true
		r.Operations = slices.Clone(r.Operations)
		routes[r.RequestChannel] = r
	}
	for i := 0; i < 2; i++ {
		select {
		case slots <- struct{}{}:
		default:
			if i == 1 {
				<-slots
			}
			return nil, errRedisConnection
		}
	}
	ctx, cancel := context.WithCancel(parent)
	p := &redisPair{ctx: ctx, cancel: cancel, closed: make(chan struct{}), slots: slots, routes: routes}
	p.stop = context.AfterFunc(ctx, func() { p.closeSockets(); close(p.closed) })
	setup, done := context.WithTimeout(ctx, redisDeadline)
	defer done()
	deadline, _ := setup.Deadline()
	var err error
	p.publisher, p.pubReader, err = p.connect(setup, b.Address, security, b.Publisher, publisherPassword, deadline)
	if err == nil {
		p.subscriber, p.subReader, err = p.connect(setup, b.Address, security, b.Subscriber, subscriberPassword, deadline)
	}
	if err == nil {
		err = p.subscribe(b.Routes, deadline)
	}
	if err != nil || ctx.Err() != nil {
		p.close()
		return nil, errRedisConnection
	}
	return p, nil
}

func (p *redisPair) connect(ctx context.Context, address string, security *tls.Config, credential config.RedisCredential, password []byte, deadline time.Time) (*tls.Conn, *bufio.Reader, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, nil, errRedisConnection
	}
	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		_ = raw.Close()
		return nil, nil, errRedisConnection
	}
	p.raw = append(p.raw, raw)
	p.mu.Unlock()
	security = security.Clone()
	security.MinVersion = max(security.MinVersion, tls.VersionTLS13)
	c := tls.Client(raw, security)
	if c.SetDeadline(deadline) != nil || c.HandshakeContext(ctx) != nil {
		return nil, nil, errRedisConnection
	}
	r := bufio.NewReader(c)
	if redisWrite(c, redisCommand([]byte("AUTH"), []byte(credential.Username), password), deadline) != nil {
		return nil, nil, errRedisConnection
	}
	v, err := redisRead(c, r, deadline)
	if err != nil || !redisOK(v) {
		return nil, nil, errRedisConnection
	}
	return c, r, nil
}

func (p *redisPair) closeSockets() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.raw {
		_ = c.Close()
	}
}

func (p *redisPair) close() {
	p.once.Do(func() {
		p.cancel()
		if !p.stop() {
			<-p.closed
		}
		p.closeSockets()
		<-p.slots
		<-p.slots
	})
}
