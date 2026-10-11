package adapter

import (
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const statefulSessionTimeout = 30 * time.Second

type mcpInitializationKey struct{}
type mcpInitialization struct {
	server *mcp.Server
	// author is set for a plain-author session: its source, header
	// password and, once created, its connection session.
	author *mcpAuthorIngress
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
	toClose := make([]*mcp.ServerSession, 0, len(m.sessionsSDK))
	for session := range m.sessionsSDK {
		toClose = append(toClose, session)
	}
	for server := range m.servers {
		for session := range server.Sessions() {
			toClose = append(toClose, session)
		}
	}
	m.mu.Unlock()
	seen := map[*mcp.ServerSession]bool{}
	for _, session := range toClose {
		if !seen[session] {
			seen[session] = true
			_ = session.Close()
		}
	}
	m.wg.Wait()
	// An initialization may connect after the first snapshot. HTTP admission
	// is now joined, so no new SDK session can appear after this second pass.
	m.closeSessions()
	m.sessionWG.Wait()
	m.mu.Lock()
	m.sessions = map[string]string{}
	m.global = 0
	m.per = map[string]int{}
	m.servers = map[*mcp.Server]struct{}{}
	m.mu.Unlock()
	close(done)
}

func (m *MCPHTTP) trackServer(server *mcp.Server) bool {
	if server == nil {
		return false
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	m.servers[server] = struct{}{}
	m.mu.Unlock()
	return true
}

func (m *MCPHTTP) finishInitialization(owner string, server *mcp.Server, author *mcpAuthorIngress) {
	var session *mcp.ServerSession
	if server != nil {
		for candidate := range server.Sessions() {
			session = candidate
			break
		}
	}
	if session == nil {
		m.mu.Lock()
		delete(m.servers, server)
		m.mu.Unlock()
		m.releaseSession(owner, "")
		author.close()
		return
	}
	id := session.ID()
	m.mu.Lock()
	if author != nil {
		m.authors[session] = author
	}
	m.sessions[id] = owner
	m.sessionsSDK[session] = server
	m.sessionWG.Add(1)
	closed := m.closed
	m.mu.Unlock()
	go m.waitSession(session)
	if closed {
		_ = session.Close()
	}
}

func (m *MCPHTTP) waitSession(session *mcp.ServerSession) {
	_ = session.Wait()
	// Wait joins RPC work; Close also invokes the HTTP handler's public
	// session-deletion hook, including when RPC closed without DELETE.
	_ = session.Close()
	m.mu.Lock()
	server, tracked := m.sessionsSDK[session]
	owner := m.sessions[session.ID()]
	if tracked {
		delete(m.sessionsSDK, session)
		delete(m.servers, server)
	}
	author := m.authors[session]
	delete(m.authors, session)
	m.mu.Unlock()
	// The session's identified author is forgotten with it.
	author.close()
	if tracked && session.ID() != "" {
		m.releaseSession(owner, session.ID())
	}
	m.sessionWG.Done()
}

func (m *MCPHTTP) closeSessions() {
	m.mu.Lock()
	var sessions []*mcp.ServerSession
	for server := range m.servers {
		for session := range server.Sessions() {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
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
