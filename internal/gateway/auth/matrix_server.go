package auth

import (
	"crypto/tls"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// MatrixServer owns immutable verified-server provisioning and credentials.
// Callers must join all credential users before Close clears the token.
// HTTP authentication, event proofs and service ownership remain separate.
type MatrixServer struct {
	address, tlsServerName, account, network string
	security                                 *tls.Config
	token                                    []byte
	freshness                                time.Duration
	routes                                   map[string]bridgeRoute // room ID
	authors                                  *BridgeAuthors
}

func NewMatrixServer(c config.Config, b config.MatrixBridge) (*MatrixServer, error) {
	if c.ValidateMatrixBridge(b) != nil {
		return nil, ErrCredential
	}
	s := &MatrixServer{address: b.Address, tlsServerName: b.TLSServerName, account: b.Account, network: b.ID,
		freshness: time.Duration(b.FreshnessSeconds) * time.Second,
		security:  &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}}, routes: make(map[string]bridgeRoute, len(b.Routes))}
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
	_, homeserver, _ := strings.Cut(b.Account, ":")
	var err error
	s.authors, err = NewBridgeAuthors(b.ID, config.BridgeGuestPrefix(b.GuestPrefix, config.DefaultMatrixGuestPrefix), homeserver, b.GuestsAllowed(), b.Accounts)
	if err != nil {
		return nil, ErrCredential
	}
	if b.TLSCAFile != "" {
		s.security.RootCAs, err = ReadTrustRoots(b.TLSCAFile)
		if err != nil {
			return nil, ErrCredential
		}
	}
	s.token, err = readIRCPassword(b.AccessTokenFile)
	if err != nil {
		return nil, ErrCredential
	}
	if !validMatrixToken(s.token) {
		s.Close()
		return nil, ErrCredential
	}
	ready = true
	return s, nil
}

func (s *MatrixServer) Close() {
	if s != nil {
		clear(s.token)
		s.token = nil
	}
}

func validMatrixToken(token []byte) bool {
	if len(token) == 0 || len(token) > 1024 {
		return false
	}
	padding := false
	for _, c := range token {
		if c == '=' {
			padding = true
			continue
		}
		if padding || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' || c == '+' || c == '/') {
			return false
		}
	}
	return token[0] != '='
}
