package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/protocol"
)

// EnableAccess requires a connection credential before any registration or data
// commands. It does not grant operator privileges or change DM audit visibility.
func (s *Server) EnableAccess(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.accessEnabled || !admin.ValidToken(token) {
		return errors.New("valid access credential must be configured once, before Serve")
	}
	if s.adminEnabled && sha256.Sum256([]byte(token)) == s.adminHash {
		return errors.New("admin and access credentials must be different")
	}
	s.accessHash, s.accessEnabled = sha256.Sum256([]byte(token)), true
	return nil
}

func (s *Server) validateListenerLocked(listener net.Listener) error {
	addr, tcp := listener.Addr().(*net.TCPAddr)
	if !tcp {
		if s.cfg.TLSConfig != nil {
			return errors.New("TLS requires a TCP listener")
		}
		return nil
	}
	if !addr.IP.IsLoopback() && (s.cfg.TLSConfig == nil || !s.accessEnabled) {
		return errors.New("remote TCP listeners require TLS and an access token; use loopback for local access")
	}
	return nil
}

// maxCredentialFailures is how many wrong credentials one connection may send
// (PASS, AUTH/REGISTER, SASL or OPER) before the server closes it.
const maxCredentialFailures = 3

// credentialFailedLocked answers a rejected credential and, on the last
// permitted failure, says why and ends the session once that reply is sent.
func (s *Server) credentialFailedLocked(client *session, code string, params []string, text string) {
	s.numericLocked(client, code, params, text)
	if client.failures++; client.failures < maxCredentialFailures {
		return
	}
	s.logger.Warn("credential_failures", "id", client.client.ID, "remote", client.conn.RemoteAddr().String())
	client.quitReason = "Too many failed credential attempts"
	client.enqueue(":server ERROR :too many failed credential attempts\r\n")
	client.closeAfterFlush()
}

func (s *Server) passLocked(client *session, command protocol.Command) {
	if client.registered {
		s.numericLocked(client, "462", nil, "Already registered")
		return
	}
	token := command.Trailing
	if token == "" {
		token, _ = command.Param(0)
	}
	hash := sha256.Sum256([]byte(token))
	if !s.accessEnabled || subtle.ConstantTimeCompare(hash[:], s.accessHash[:]) != 1 {
		s.credentialFailedLocked(client, "464", nil, "Invalid connection credential or access authentication disabled")
		return
	}
	client.access = true
	s.numericLocked(client, "782", nil, "Connection authenticated")
}
