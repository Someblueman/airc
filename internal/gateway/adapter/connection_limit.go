package adapter

import (
	"net"
	"sync"
)

// LimitConnections bounds a listener's open connections, IRC style: at most
// total at once, and at most perSource from one client IP address. Carriers
// without addresses (Unix sockets) have only the total. A refused connection
// is closed at once, before any protocol work. As in aircd, loopback peers
// are local and have only the total.
func LimitConnections(l net.Listener, total, perSource int) net.Listener {
	return &connectionLimit{Listener: l, total: total, perSource: perSource, bySource: map[string]int{}}
}

// exemptLoopback is false only in tests, which have no other address.
var exemptLoopback = true

type connectionLimit struct {
	net.Listener
	mu        sync.Mutex
	total     int
	perSource int
	open      int
	bySource  map[string]int
}

func (l *connectionLimit) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		key := ""
		if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok && !(exemptLoopback && addr.IP.IsLoopback()) {
			key = addr.IP.String()
		}
		l.mu.Lock()
		refused := l.open >= l.total || key != "" && l.bySource[key] >= l.perSource
		if !refused {
			l.open++
			if key != "" {
				l.bySource[key]++
			}
		}
		l.mu.Unlock()
		if refused {
			_ = conn.Close()
			continue
		}
		return &limitedConnection{Conn: conn, owner: l, key: key}, nil
	}
}

func (l *connectionLimit) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.open--
	if key != "" {
		if l.bySource[key]--; l.bySource[key] <= 0 {
			delete(l.bySource, key)
		}
	}
}

type limitedConnection struct {
	net.Conn
	owner *connectionLimit
	key   string
	once  sync.Once
}

func (c *limitedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.release(c.key) })
	return err
}
