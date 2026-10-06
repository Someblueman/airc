package adapter

import (
	"context"
	"net"
	"sync"
)

type mqttSession struct {
	conn   net.Conn
	cancel context.CancelFunc
	done   chan struct{}
}

type mqttSessions struct {
	mu     sync.Mutex
	active map[string]*mqttSession
}

// claim replaces only an already authenticated owner. Join the predecessor
// outside the mutex so its release cannot deadlock a reconnect chain.
func (o *mqttSessions) claim(ctx context.Context, principal string, next *mqttSession) bool {
	o.mu.Lock()
	old := o.active[principal]
	o.active[principal] = next
	o.mu.Unlock()
	if old != nil {
		old.cancel()
		closeMQTTConnection(old.conn)
		select {
		case <-old.done:
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}

func (o *mqttSessions) release(principal string, session *mqttSession) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active[principal] == session {
		delete(o.active, principal)
	}
}
