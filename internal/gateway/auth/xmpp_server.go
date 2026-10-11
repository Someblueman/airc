package auth

import (
	"crypto/tls"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// XMPPServer owns immutable verified-server provisioning and credentials.
// Callers must join all Connect/session users before Close clears credentials.
// The service owns serial sessions and joins them before credential cleanup.
type XMPPServer struct {
	address, account, resource, network string
	security                            *tls.Config
	password                            []byte
	freshness                           time.Duration
	routes                              map[string]bridgeRoute // room bare JID
	authors                             *BridgeAuthors
}

func NewXMPPServer(c config.Config, b config.XMPPBridge) (*XMPPServer, error) {
	if c.ValidateXMPPBridge(b) != nil {
		return nil, ErrCredential
	}
	_, domain, _ := strings.Cut(b.Account, "@")
	if b.TLSServerName != domain {
		return nil, ErrCredential
	}
	s := &XMPPServer{address: b.Address, account: b.Account, resource: b.Resource, network: b.ID,
		freshness: time.Duration(b.FreshnessSeconds) * time.Second,
		security:  &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"xmpp-client"}}, routes: make(map[string]bridgeRoute, len(b.Routes))}
	ready := false
	defer func() {
		if !ready {
			s.Close()
		}
	}()
	for _, r := range b.Routes {
		route, ok := newBridgeRoute(r)
		if !ok {
			return nil, ErrCredential
		}
		s.routes[r.Room] = route
	}
	var err error
	s.authors, err = NewBridgeAuthors(b.ID, config.BridgeGuestPrefix(b.GuestPrefix, config.DefaultXMPPGuestPrefix), domain, b.GuestsAllowed(), b.Accounts)
	if err != nil {
		return nil, ErrCredential
	}
	if b.TLSCAFile != "" {
		s.security.RootCAs, err = ReadTrustRoots(b.TLSCAFile)
		if err != nil {
			return nil, ErrCredential
		}
	}
	s.password, err = readIRCPassword(b.PasswordFile)
	if err != nil {
		return nil, ErrCredential
	}
	if !xmppwire.ValidPassword(s.password) {
		s.Close()
		return nil, ErrCredential
	}
	ready = true
	return s, nil
}

func (s *XMPPServer) Close() {
	if s != nil {
		clear(s.password)
		s.password = nil
	}
}
