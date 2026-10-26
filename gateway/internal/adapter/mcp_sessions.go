package adapter

import (
	"time"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

const statefulSessionTimeout = 30 * time.Second

type mcpInitializationKey struct{}
type mcpInitialization struct {
	// author is set for a plain-author session: its source, header
	// password and, once created, its connection session.
	author *mcpAuthorIngress
	owner  string
	// started is set once the MCP handler has begun the session.
	started bool
}

type mcpAuthorIngress struct{ dispatch.Ingress }

func (a *mcpAuthorIngress) close() {
	if a != nil {
		a.Session.Close()
	}
}

func (m *MCPHTTP) acquireRequest(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *MCPHTTP) acquireSession(owner string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.global >= m.max || m.per[owner] >= m.maxPer {
		return false
	}
	m.global++
	m.per[owner]++
	return true
}

func (m *MCPHTTP) releaseSession(owner, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sessionID != "" {
		if holder, ok := m.sessions[sessionID]; !ok || holder != owner {
			return
		} else {
			delete(m.sessions, sessionID)
		}
	}
	if m.global > 0 {
		m.global--
	}
	if m.per[owner] > 0 {
		m.per[owner]--
	}
}

func (m *MCPHTTP) isClosed() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.closed }

func (m *MCPHTTP) Close() {
	m.mu.Lock()
	if m.closed {
		done := m.closedCh
		m.mu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	if m.closedCh == nil {
		m.closedCh = make(chan struct{})
	}
	done := m.closedCh
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	// The handler refuses new sessions from here on, then ends and joins
	// every session; their hooks release admission.
	if m.handler != nil {
		m.handler.Close()
	}
	m.wg.Wait()
	m.mu.Lock()
	m.sessions = map[string]string{}
	m.global = 0
	m.per = map[string]int{}
	m.mu.Unlock()
	close(done)
}

// openSession records the owner of an initialized session, so that only its
// source can use the session ID.
func (m *MCPHTTP) openSession(owner, id string) {
	m.mu.Lock()
	m.sessions[id] = owner
	m.mu.Unlock()
}

func (m *MCPHTTP) beginRequest() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.wg.Add(1)
	return true
}
