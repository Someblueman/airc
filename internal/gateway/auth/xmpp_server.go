package auth

import (
	"crypto/tls"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

type xmppGrant struct {
	identity   Identity
	target     core.Target
	operations []core.Operation
}

// XMPPServer owns immutable verified-server provisioning and credentials.
// Callers must join all Connect/session users before Close clears credentials.
// It provisions private session proofs; service ownership remains separate.
type XMPPServer struct {
	address, account, resource, network string
	security                            *tls.Config
	password                            []byte
	freshness                           time.Duration
	grants                              map[[2]string]xmppGrant
	verifier                            *Verifier
}

func NewXMPPServer(v *Verifier, c config.Config, b config.XMPPBridge) (*XMPPServer, error) {
	if v == nil || c.ValidateXMPPBridge(b) != nil {
		return nil, ErrCredential
	}
	_, domain, _ := strings.Cut(b.Account, "@")
	if b.TLSServerName != domain {
		return nil, ErrCredential
	}
	s := &XMPPServer{address: b.Address, account: b.Account, resource: b.Resource, network: b.ID, verifier: v,
		freshness: time.Duration(b.FreshnessSeconds) * time.Second,
		security:  &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"xmpp-client"}}, grants: make(map[[2]string]xmppGrant, len(b.Routes))}
	for _, r := range b.Routes {
		var identity Identity
		for i := range v.principals {
			candidate := Identity{&v.principals[i]}
			if candidate.ID() == r.Principal && v.OwnsIdentity(candidate) {
				identity = candidate
				break
			}
		}
		if identity.ID() == "" {
			return nil, ErrCredential
		}
		for _, op := range r.Operations {
			if !slices.Contains(identity.principal.operations, op) || !slices.Contains(identity.principal.targets, r.Target) {
				return nil, ErrCredential
			}
		}
		s.grants[[2]string{r.Author, r.Room}] = xmppGrant{identity: identity, target: r.Target, operations: slices.Clone(r.Operations)}
	}
	var err error
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
	return s, nil
}

func (s *XMPPServer) Close() {
	if s != nil {
		clear(s.password)
		s.password = nil
	}
}
