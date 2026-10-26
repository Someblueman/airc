package adapter

import (
	"context"
	"errors"
	"net"
	"sync"
)

// errMQTTClientIDHeld refuses a connection whose client ID an identified
// author's connection holds: only that author may replace it.
var errMQTTClientIDHeld = errors.New("the client ID belongs to another author")

type mqttSession struct {
	conn   net.Conn
	cancel context.CancelFunc
	done   chan struct{}
	// takeover, when set, tells the client a newer connection replaced it
	// (MQTT 5's DISCONNECT 0x8E) before the connection closes.
	takeover func()
	// owner is the author scope the connection identified as ("" for
	// none), guarded by mqttSessions.mu.
	owner string
}

type mqttSessions struct {
	mu     sync.Mutex
	active map[string]*mqttSession
}

// claim makes next the connection for clientID, as owner, replacing the
// one that holds it: any connection may replace one without an author, but
// only the same author may replace an identified one, so a client ID alone
// cannot end another author's connection. Join the predecessor outside the
// mutex so its release cannot deadlock a reconnect chain.
func (o *mqttSessions) claim(ctx context.Context, clientID, owner string, next *mqttSession) error {
	o.mu.Lock()
	old := o.active[clientID]
	if old != nil && old.owner != "" && old.owner != owner {
		o.mu.Unlock()
		return errMQTTClientIDHeld
	}
	next.owner = owner
	o.active[clientID] = next
	o.mu.Unlock()
	if old != nil {
		if old.takeover != nil {
			old.takeover()
		}
		old.cancel()
		closeMQTTConnection(old.conn)
		select {
		case <-old.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// identify records that session, which held no author, now acts as owner.
func (o *mqttSessions) identify(session *mqttSession, owner string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if session.owner == "" {
		session.owner = owner
	}
}

func (o *mqttSessions) release(owner string, session *mqttSession) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active[owner] == session {
		delete(o.active, owner)
	}
}
