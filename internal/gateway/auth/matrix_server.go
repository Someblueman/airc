package auth

import (
	"crypto/ecdsa"
	"crypto/tls"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

type matrixGrant struct {
	identity                   Identity
	target                     core.Target
	operations                 []core.Operation
	responseRecipientKeyID     string
	responseRecipientPublicKey *ecdsa.PublicKey
}

// MatrixServer owns immutable verified-server provisioning and credentials.
// Callers must join all credential users before Close clears the token.
// HTTP authentication, event proofs and service ownership remain separate.
type MatrixServer struct {
	address, tlsServerName, account, network, realm string
	security                                        *tls.Config
	token                                           []byte
	freshness                                       time.Duration
	grants                                          map[[2]string]matrixGrant
	verifier                                        *Verifier
	responseSealer                                  *ResponseSealer
}

func NewMatrixServer(v *Verifier, c config.Config, b config.MatrixBridge) (*MatrixServer, error) {
	if v == nil || c.ValidateMatrixBridge(b) != nil {
		return nil, ErrCredential
	}
	s := &MatrixServer{address: b.Address, tlsServerName: b.TLSServerName, account: b.Account, network: b.ID, realm: c.Realm, verifier: v,
		freshness: time.Duration(b.FreshnessSeconds) * time.Second,
		security:  &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}}, grants: make(map[[2]string]matrixGrant, len(b.Routes))}
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
		grant := matrixGrant{identity: identity, target: r.Target, operations: slices.Clone(r.Operations)}
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
		if s.responseSealer != nil {
			clear(s.responseSealer.signingKey)
			s.responseSealer.signingKey = nil
			s.responseSealer = nil
		}
	}
}

// MatrixResponseRoute binds a protected result to this bridge, author, room
// and fixed native target. It does not create an external Matrix identity.
func MatrixResponseRoute(network, author, room string, target core.Target) string {
	targetCopy := target
	if !validMatrixNetworkID(network) || !matrixwire.UserID(author) || !matrixwire.RoomID(room) || targetCopy.Normalize() != nil || targetCopy != target {
		return ""
	}
	return ircDigest("airc-matrix-response-v1", network, author, room, targetCopy.Kind, targetCopy.Name)
}

func validMatrixNetworkID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
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
