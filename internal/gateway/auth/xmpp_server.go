package auth

import (
	"crypto/ecdsa"
	"crypto/tls"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

type xmppGrant struct {
	identity                   Identity
	target                     core.Target
	operations                 []core.Operation
	responseRecipientKeyID     string
	responseRecipientPublicKey *ecdsa.PublicKey
}

// XMPPServer owns immutable verified-server provisioning and credentials.
// Callers must join all Connect/session users before Close clears credentials.
// The service owns serial sessions and joins them before credential cleanup.
type XMPPServer struct {
	address, account, resource, network, realm string
	security                                   *tls.Config
	password                                   []byte
	freshness                                  time.Duration
	grants                                     map[[2]string]xmppGrant
	verifier                                   *Verifier
	responseSealer                             *ResponseSealer
}

func NewXMPPServer(v *Verifier, c config.Config, b config.XMPPBridge) (*XMPPServer, error) {
	if v == nil || c.ValidateXMPPBridge(b) != nil {
		return nil, ErrCredential
	}
	_, domain, _ := strings.Cut(b.Account, "@")
	if b.TLSServerName != domain {
		return nil, ErrCredential
	}
	s := &XMPPServer{address: b.Address, account: b.Account, resource: b.Resource, network: b.ID, realm: c.Realm, verifier: v,
		freshness: time.Duration(b.FreshnessSeconds) * time.Second,
		security:  &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"xmpp-client"}}, grants: make(map[[2]string]xmppGrant, len(b.Routes))}
	ready := false
	defer func() {
		if !ready {
			s.Close()
		}
	}()
	hasRecipients := false
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
		grant := xmppGrant{identity: identity, target: r.Target, operations: slices.Clone(r.Operations)}
		if r.ResponseRecipientKeyID != "" || r.ResponseRecipientPublicKeyFile != "" {
			if r.ResponseRecipientKeyID == "" || r.ResponseRecipientPublicKeyFile == "" {
				return nil, ErrCredential
			}
			key, err := ReadResponseRecipientKey(r.ResponseRecipientPublicKeyFile)
			if err != nil {
				return nil, ErrCredential
			}
			grant.responseRecipientKeyID = r.ResponseRecipientKeyID
			grant.responseRecipientPublicKey = key
			hasRecipients = true
		}
		s.grants[[2]string{r.Author, r.Room}] = grant
	}
	var err error
	if b.TLSCAFile != "" {
		s.security.RootCAs, err = ReadTrustRoots(b.TLSCAFile)
		if err != nil {
			return nil, ErrCredential
		}
	}
	if hasRecipients {
		if c.ResponseSigningKey == nil {
			return nil, ErrCredential
		}
		signingKey, err := ReadResponseSigningKey(c.ResponseSigningKey.PrivateKeyFile)
		if err != nil {
			return nil, ErrCredential
		}
		s.responseSealer, err = NewResponseSealer(c.ResponseSigningKey.ID, signingKey)
		clear(signingKey)
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
		if s.responseSealer != nil {
			clear(s.responseSealer.signingKey)
			s.responseSealer.signingKey = nil
			s.responseSealer = nil
		}
	}
}
