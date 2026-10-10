package adapter

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeDCCP serves connected record sockets using the packet envelope profile.
// Each connection owns its assembler and only the provisioned routes matching
// its peer. The shared session slot bounds both the connection and its source.
func (u *PacketDatagrams) ServeDCCP(parent context.Context, listener net.Listener) error {
	if u == nil || u.network != "dccp" || listener == nil || listener.Addr() == nil || listener.Addr().Network() != "dccp" {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	var workers sync.WaitGroup
	defer func() {
		cancel()
		_ = listener.Close()
		if !stop() {
			<-closed
		}
		workers.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		packets, ok := conn.(net.PacketConn)
		if !ok {
			_ = conn.Close()
			return store.ErrInvalid
		}
		var routes []PacketReturnRoute
		for _, route := range u.routes {
			if packetAddrKey(route.Destination) == packetAddrKey(conn.RemoteAddr()) {
				routes = append(routes, route)
			}
		}
		if len(routes) == 0 || ctx.Err() != nil {
			_ = conn.Close()
			continue
		}
		select {
		case u.slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		// A connected socket has exactly one source; its global slot is already
		// held above. Do not charge another global slot for fragment assembly.
		driver, err := NewPacketDatagrams(u.d, u.sealer, u.realm, routes, make(chan struct{}, 1), u.requests)
		if err != nil {
			<-u.slots
			_ = conn.Close()
			return err
		}
		workers.Go(func() {
			defer func() { <-u.slots }()
			_ = driver.Serve(ctx, &dccpSession{PacketConn: packets, idle: time.Now().Add(5 * time.Second)})
		})
	}
}

// PacketDatagrams polls deadlines to reap fragment sources. Translate the
// session idle boundary into EOF so a silent DCCP peer cannot retain a slot.
// Only the read goroutine touches idle; writes retain independent deadlines.
type dccpSession struct {
	net.PacketConn
	idle time.Time
}

func (c *dccpSession) SetReadDeadline(deadline time.Time) error {
	if deadline.IsZero() || deadline.After(c.idle) {
		deadline = c.idle
	}
	return c.PacketConn.SetReadDeadline(deadline)
}

func (c *dccpSession) ReadFrom(payload []byte) (int, net.Addr, error) {
	n, peer, err := c.PacketConn.ReadFrom(payload)
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() && !time.Now().Before(c.idle) {
		return 0, nil, io.EOF
	}
	return n, peer, err
}
