package adapter

import (
	"context"
	"crypto/tls"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

// kafkaConn has one serial command owner. Cancellation closes the raw socket;
// close joins both cancellation and session expiry before releasing capacity.
type kafkaConn struct {
	ctx         context.Context
	cancel      context.CancelFunc
	stop        func() bool
	closed      chan struct{}
	once        sync.Once
	mu          sync.Mutex
	raw         net.Conn
	conn        *tls.Conn
	slots       chan struct{}
	correlation int32
	expiry      time.Time
	timer       *time.Timer
	expired     chan struct{}
}

func openKafka(parent context.Context, b config.KafkaBroker, security *tls.Config, credential config.KafkaCredential, password []byte, slots chan struct{}) (*kafkaConn, error) {
	authBody, err := kafkawire.PlainAuthenticate(credential.Username, password)
	if err != nil {
		return nil, errKafkaConnection
	}
	defer clear(authBody)
	host, port, err := net.SplitHostPort(b.Address)
	ip := net.ParseIP(host)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || ip == nil || ip.IsUnspecified() || number < 1 || number > 65535 || b.ID < 0 || security == nil || security.InsecureSkipVerify || security.ServerName == "" || security.ServerName != b.TLSServerName || cap(slots) < 1 || cap(slots) > 256 {
		return nil, errKafkaConnection
	}
	select {
	case slots <- struct{}{}:
	default:
		return nil, errKafkaConnection
	}
	ctx, cancel := context.WithCancel(parent)
	p := &kafkaConn{ctx: ctx, cancel: cancel, closed: make(chan struct{}), slots: slots}
	p.stop = context.AfterFunc(ctx, func() { p.closeSocket(); close(p.closed) })
	setup, done := context.WithTimeout(ctx, kafkaDeadline)
	defer done()
	deadline, _ := setup.Deadline()
	raw, err := (&net.Dialer{}).DialContext(setup, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(number)))
	if err == nil {
		p.mu.Lock()
		if ctx.Err() != nil {
			_ = raw.Close()
			err = errKafkaConnection
		} else {
			p.raw = raw
		}
		p.mu.Unlock()
	}
	if err == nil {
		security = security.Clone()
		security.MinVersion = max(security.MinVersion, tls.VersionTLS13)
		p.conn = tls.Client(raw, security)
		if p.conn.SetDeadline(deadline) != nil || p.conn.HandshakeContext(setup) != nil {
			err = errKafkaConnection
		} else {
			err = p.authenticate(authBody, deadline)
		}
	}
	if err != nil || ctx.Err() != nil {
		p.close()
		return nil, errKafkaConnection
	}
	return p, nil
}

func (p *kafkaConn) authenticate(authBody []byte, deadline time.Time) error {
	reply, err := p.command(18, 0, nil, deadline)
	if err != nil || kafkawire.VersionsReply(reply) != nil {
		return errKafkaConnection
	}
	reply, err = p.command(17, 1, kafkawire.PlainHandshake(), deadline)
	if err != nil || kafkawire.PlainHandshakeReply(reply) != nil {
		return errKafkaConnection
	}
	started := time.Now()
	reply, err = p.command(36, 1, authBody, deadline)
	if err != nil {
		return err
	}
	lifetime, err := kafkawire.PlainAuthenticateReply(reply)
	if err != nil || lifetime > math.MaxInt64/int64(time.Millisecond) {
		return errKafkaConnection
	}
	if lifetime > 0 {
		p.expiry = started.Add(time.Duration(lifetime) * time.Millisecond)
		if !time.Now().Before(p.expiry) {
			return errKafkaConnection
		}
		p.expired = make(chan struct{})
		p.timer = time.AfterFunc(time.Until(p.expiry), func() { p.cancel(); close(p.expired) })
	}
	return nil
}

func (p *kafkaConn) command(api, version int16, body []byte, deadline time.Time) ([]byte, error) {
	if !p.ready() || p.correlation == math.MaxInt32 {
		p.close()
		return nil, errKafkaConnection
	}
	correlation := p.correlation
	p.correlation++
	reply, err := kafkaCommand(p.conn, api, version, correlation, body, stompEarlier(stompEarlier(time.Now().Add(kafkaDeadline), deadline), p.expiry))
	if err != nil || !p.ready() {
		p.close()
		return nil, errKafkaConnection
	}
	return reply, nil
}

func (p *kafkaConn) closeSocket() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.raw != nil {
		_ = p.raw.Close()
	}
}
func (p *kafkaConn) close() {
	p.once.Do(func() {
		p.cancel()
		if p.timer != nil && !p.timer.Stop() {
			<-p.expired
		}
		if !p.stop() {
			<-p.closed
		}
		p.closeSocket()
		<-p.slots
	})
}

// ready checks expiry directly; timer scheduling cannot grant a ready session.
func (p *kafkaConn) ready() bool {
	return p != nil && p.ctx != nil && p.ctx.Err() == nil && (p.expiry.IsZero() || time.Now().Before(p.expiry))
}
