package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/protocol"
)

// EnableGateway lets a connection presenting this credential assert the real
// source of the client it carries, with WEBIRC, so per-address limits apply to
// that client rather than to the gateway's own (often loopback) connection.
func (s *Server) EnableGateway(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.gatewayEnabled || !admin.ValidToken(token) {
		return errors.New("valid gateway credential must be configured once, before Serve")
	}
	hash := sha256.Sum256([]byte(token))
	if s.accessEnabled && hash == s.accessHash || s.adminEnabled && hash == s.adminHash {
		return errors.New("gateway credential must differ from the access and admin credentials")
	}
	s.gatewayHash, s.gatewayEnabled = hash, true
	return nil
}

// sourceKey is the per-address limit key for an asserted source: an address
// grouped as for a direct peer, or the label itself. Asserted loopback
// addresses are not exempt: the client reached aircd only through the gateway.
func sourceKey(source string) string {
	if ip := net.ParseIP(source); ip != nil {
		return addressKey(ip)
	}
	return source
}

// webircLocked handles WEBIRC <source> :<gateway-token>, which must be a
// connection's first command. Every failure ends the connection, so a gateway
// can pipeline its client's commands without them running unattributed.
func (s *Server) webircLocked(client *session, command protocol.Command) {
	refuse := func(reason string) {
		client.quitReason = "Source not accepted"
		client.enqueue(":server ERROR :" + reason + "\r\n")
		client.closeAfterFlush()
	}
	if client.settled {
		s.numericLocked(client, "462", nil, "WEBIRC must be the first command")
		refuse("source not accepted")
		return
	}
	client.settled = true
	if s.credentialThrottledLocked(client, "464") {
		refuse("source not accepted")
		return
	}
	hash := sha256.Sum256([]byte(command.Trailing))
	if subtle.ConstantTimeCompare(hash[:], s.gatewayHash[:]) != 1 {
		s.credentialFailedLocked(client, "464", nil, "Invalid gateway credential")
		if !client.finishing {
			refuse("source not accepted")
		}
		return
	}
	source, _ := command.Param(0)
	if !protocol.ValidSource(source) {
		s.numericLocked(client, "461", []string{"WEBIRC"}, "Source must be an IP address or a label")
		refuse("source not accepted")
		return
	}
	key := sourceKey(source)
	s.releasePendingLocked(client)
	if s.pending[key] >= s.cfg.MaxPendingPerAddress {
		s.logger.Debug("client_refused", "id", client.client.ID, "source", source, "reason", "too many unregistered connections from address")
		refuse("server is full or shutting down")
		return
	}
	s.pending[key]++
	client.pendingKey, client.remoteKey, client.source = key, key, source
	s.logger.Info("client_source", "id", client.client.ID, "source", source)
	s.numericLocked(client, "786", []string{source}, "Source accepted")
}
