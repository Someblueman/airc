package main

import (
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type faultResult struct {
	At        time.Time `json:"at"`
	Requested int       `json:"requested"`
	Closed    int       `json:"closed"`
}

// The wrapper is only used by the isolated load server. No production server
// interfaces or administrative permissions are changed to inject a socket reset.
type faultListener struct {
	net.Listener
	mu     sync.Mutex
	next   int
	active map[int]*faultConn
}

type faultConn struct {
	net.Conn
	owner  *faultListener
	id     int
	closed atomic.Bool
}

func (l *faultListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil {
		l.active = map[int]*faultConn{}
	}
	l.next++
	f := &faultConn{Conn: c, owner: l, id: l.next}
	l.active[f.id] = f
	return f, nil
}

func (c *faultConn) close(reset bool) (bool, error) {
	if !c.closed.CompareAndSwap(false, true) {
		return false, nil
	}
	c.owner.mu.Lock()
	delete(c.owner.active, c.id)
	c.owner.mu.Unlock()
	if reset {
		if tcp, ok := c.Conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
	}
	return true, c.Conn.Close()
}
func (c *faultConn) Close() error { _, err := c.close(false); return err }

func (l *faultListener) disconnect(count int) faultResult {
	r := faultResult{At: time.Now().UTC(), Requested: count}
	l.mu.Lock()
	var ids []int
	for id := range l.active {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var selected []*faultConn
	for _, id := range ids {
		selected = append(selected, l.active[id])
	}
	l.mu.Unlock()
	for _, c := range selected {
		if r.Closed == count {
			break
		}
		if closed, _ := c.close(true); closed {
			r.Closed++
		}
	}
	return r
}
