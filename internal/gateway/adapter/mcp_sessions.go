package adapter

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net"
	"time"
)

const statefulSessionTimeout = 30 * time.Second

type mcpInitializationKey struct{}
type mcpInitialization struct{ server *mcp.Server }

func netLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && net.ParseIP(host).IsLoopback()
}

func (m *MCPHTTP) acquireRequest(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *MCPHTTP) acquireSession(principal string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.global >= m.max || m.per[principal] >= m.maxPer {
		return false
	}
	m.global++
	m.per[principal]++
	return true
}

func (m *MCPHTTP) releaseSession(principal, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sessionID != "" {
		if owner, ok := m.sessions[sessionID]; !ok || owner != principal {
			return
		} else {
			delete(m.sessions, sessionID)
		}
	}
	if m.global > 0 {
		m.global--
	}
	if m.per[principal] > 0 {
		m.per[principal]--
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

func (m *MCPHTTP) finishInitialization(principal string, server *mcp.Server) {
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
		m.releaseSession(principal, "")
		return
	}
	id := session.ID()
	m.mu.Lock()
	m.sessions[id] = principal
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
	principal := m.sessions[session.ID()]
	if tracked {
		delete(m.sessionsSDK, session)
		delete(m.servers, server)
	}
	m.mu.Unlock()
	if tracked && session.ID() != "" {
		m.releaseSession(principal, session.ID())
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
