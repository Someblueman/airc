package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"time"

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
	if s.gatewayEnabled && sha256.Sum256([]byte(token)) == s.gatewayHash {
		return errors.New("gateway and access credentials must be different")
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

const (
	// maxCredentialFailures is how many wrong credentials one connection may
	// send (PASS, WEBIRC, AUTH/REGISTER, SASL or OPER) before the server
	// closes it.
	maxCredentialFailures = 3
	// A remote address may fail this many credentials per window, across all
	// its connections, before its credential commands are denied unchecked
	// until older failures leave the window. Reconnecting, which a gateway
	// does for every attempt, does not get around the per-connection limit.
	// Loopback and Unix-socket peers are exempt unless a gateway asserted
	// their real source with WEBIRC.
	maxFailuresPerAddress = 3 * maxCredentialFailures
	failureWindow         = 10 * time.Minute
	maxFailureAddresses   = 4096
)

// credentialFailedLocked answers a rejected credential and, on the last
// permitted failure, says why and ends the session once that reply is sent.
func (s *Server) credentialFailedLocked(client *session, code string, params []string, text string) {
	s.noteFailedAddressLocked(client.remoteKey)
	s.numericLocked(client, code, params, text)
	s.countFailureLocked(client)
}

// credentialThrottledLocked denies a credential command without checking it
// while the connection's address has failed too often recently. The denial
// counts against the connection but not again against the address.
func (s *Server) credentialThrottledLocked(client *session, code string) bool {
	key := client.remoteKey
	if key == "" || len(recentEvents(s.failedAddresses, key, s.now().Add(-failureWindow))) < maxFailuresPerAddress {
		return false
	}
	s.numericLocked(client, code, nil, "Too many failed credential attempts from this address; try again later")
	s.countFailureLocked(client)
	return true
}

func (s *Server) noteFailedAddressLocked(key string) {
	if key == "" {
		return
	}
	if _, known := s.failedAddresses[key]; !known && len(s.failedAddresses) >= maxFailureAddresses {
		cutoff := s.now().Add(-failureWindow)
		for other := range s.failedAddresses {
			recentEvents(s.failedAddresses, other, cutoff)
		}
		// Still full of live entries: forget arbitrary ones rather than grow.
		for other := range s.failedAddresses {
			if len(s.failedAddresses) < maxFailureAddresses {
				break
			}
			delete(s.failedAddresses, other)
		}
	}
	recent := append(s.failedAddresses[key], s.now())
	if len(recent) > maxFailuresPerAddress {
		recent = recent[len(recent)-maxFailuresPerAddress:]
	}
	s.failedAddresses[key] = recent
}

func (s *Server) countFailureLocked(client *session) {
	if client.failures++; client.failures < maxCredentialFailures {
		return
	}
	s.logger.Warn("credential_failures", "id", client.client.ID, "remote", client.conn.RemoteAddr().String(), "source", client.source)
	client.quitReason = "Too many failed credential attempts"
	client.enqueue(":server ERROR :too many failed credential attempts\r\n")
	client.closeAfterFlush()
}

func (s *Server) passLocked(client *session, command protocol.Command) {
	if client.registered {
		s.numericLocked(client, "462", nil, "Already registered")
		return
	}
	if s.credentialThrottledLocked(client, "464") {
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
