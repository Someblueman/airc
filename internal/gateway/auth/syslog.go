package auth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type syslogGrant struct {
	identity Identity
	target   core.Target
}

// SyslogAuthenticator owns a snapshot of provisioned certificate grants.
// Certificate pinning authenticates the transport sender, not record origin.
type SyslogAuthenticator struct{ grants map[[32]byte]syslogGrant }

func NewSyslogAuthenticator(v *Verifier, options config.Syslog) (*SyslogAuthenticator, error) {
	if v == nil || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return nil, ErrCredential
	}
	a := &SyslogAuthenticator{grants: make(map[[32]byte]syslogGrant, len(options.Routes))}
	for _, r := range options.Routes {
		decoded, err := hex.DecodeString(r.CertificateSHA256)
		if err != nil || len(decoded) != 32 || r.CertificateSHA256 != strings.ToLower(r.CertificateSHA256) {
			return nil, ErrCredential
		}
		pin := [32]byte(decoded)
		if _, ok := a.grants[pin]; ok {
			return nil, ErrCredential
		}
		var identity Identity
		for i := range v.principals {
			p := &v.principals[i]
			if p.id == r.Principal && !p.revoked && slices.Contains(p.operations, core.Send) && slices.Contains(p.targets, r.Target) {
				identity = Identity{p}
				break
			}
		}
		if identity.ID() == "" {
			return nil, ErrCredential
		}
		a.grants[pin] = syslogGrant{identity, r.Target}
	}
	return a, nil
}

// TLSConfig returns the pinned TLS 1.3 server profile. The caller owns listener
// admission and deadlines. No CA/name fallback or resumed session bypass exists.
func (a *SyslogAuthenticator) TLSConfig(base *tls.Config) (*tls.Config, error) {
	if a == nil || len(a.grants) == 0 || base == nil || len(base.Certificates) == 0 {
		return nil, ErrCredential
	}
	c := base.Clone()
	c.MinVersion, c.MaxVersion = tls.VersionTLS13, tls.VersionTLS13
	c.NextProtos = nil
	c.ClientAuth = tls.RequireAnyClientCert
	c.ClientCAs = nil
	c.SessionTicketsDisabled = true
	c.GetConfigForClient = nil
	c.VerifyPeerCertificate = nil
	c.VerifyConnection = func(state tls.ConnectionState) error { _, err := a.resolve(state); return err }
	return c, nil
}

func (a *SyslogAuthenticator) resolve(state tls.ConnectionState) (syslogGrant, error) {
	if a == nil || state.Version != tls.VersionTLS13 || len(state.PeerCertificates) == 0 {
		return syslogGrant{}, ErrAuthentication
	}
	cert := state.PeerCertificates[0]
	now := time.Now()
	if cert == nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) || cert.IsCA || len(cert.UnhandledCriticalExtensions) != 0 || cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return syslogGrant{}, ErrAuthentication
	}
	if (len(cert.ExtKeyUsage) != 0 || len(cert.UnknownExtKeyUsage) != 0) && !slices.Contains(cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth) && !slices.Contains(cert.ExtKeyUsage, x509.ExtKeyUsageAny) {
		return syslogGrant{}, ErrAuthentication
	}
	grant, ok := a.grants[sha256.Sum256(cert.Raw)]
	if !ok || grant.identity.ID() == "" {
		return syslogGrant{}, ErrAuthentication
	}
	return grant, nil
}

// Authenticate requires an actual completed TLS handshake, which proves private
// key possession. It returns the provisioned exact target, never record metadata.
func (a *SyslogAuthenticator) Authenticate(ctx context.Context, c *tls.Conn) (Identity, core.Target, error) {
	if c == nil || c.HandshakeContext(ctx) != nil {
		return Identity{}, core.Target{}, ErrAuthentication
	}
	state := c.ConnectionState()
	if !state.HandshakeComplete {
		return Identity{}, core.Target{}, ErrAuthentication
	}
	grant, err := a.resolve(state)
	if err != nil {
		return Identity{}, core.Target{}, err
	}
	return grant.identity, grant.target, nil
}
