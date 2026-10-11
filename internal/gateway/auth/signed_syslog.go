package auth

import (
	"bytes"
	//lint:ignore SA1019 RFC 5848 verification uses a bounded DSA profile, never signing.
	"crypto/dsa"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/syslogwire"
)

type signedSyslogGrant struct {
	author        RouteAuthor
	target        core.Target
	certificate   []byte
	before, after time.Time
	key           *syslogwire.BlockVerifier
	signer        [32]byte
}

// SignedSyslogAuthenticator snapshots provisioned origin certificates and
// authority. Transport peers and unsigned header fields do not confer grants.
type SignedSyslogAuthenticator struct{ grants map[string]signedSyslogGrant }

func NewSignedSyslogAuthenticator(options config.SignedSyslog) (*SignedSyslogAuthenticator, error) {
	if len(options.Routes) < 1 || len(options.Routes) > 128 {
		return nil, ErrCredential
	}
	a := &SignedSyslogAuthenticator{grants: make(map[string]signedSyslogGrant, len(options.Routes))}
	for _, r := range options.Routes {
		name := strings.ToLower(r.Hostname)
		if !config.SignedSyslogHostname(r.Hostname) {
			return nil, ErrCredential
		}
		if _, exists := a.grants[name]; exists {
			return nil, ErrCredential
		}
		cert, err := config.SignedSyslogCertificate(r.CertificateDER)
		if err != nil {
			return nil, ErrCredential
		}
		public, ok := cert.PublicKey.(*dsa.PublicKey)
		if !ok {
			return nil, ErrCredential
		}
		key, err := syslogwire.NewBlockVerifier(public)
		if err != nil {
			return nil, ErrCredential
		}
		author, err := LoadBoundAuthor(r.BoundAuthor)
		if err != nil {
			return nil, err
		}
		a.grants[name] = signedSyslogGrant{author, r.Target, bytes.Clone(cert.Raw), cert.NotBefore, cert.NotAfter, key, sha256.Sum256(cert.RawSubjectPublicKeyInfo)}
	}
	return a, nil
}

// VerifyBlock proves the configured origin key signed every covered byte.
// The returned author still requires durable freshness/replay admission.
func (a *SignedSyslogAuthenticator) VerifyBlock(raw []byte, covered [][]byte, now time.Time) (RouteAuthor, core.Target, [32]byte, syslogwire.SignedBlock, error) {
	if a == nil {
		return RouteAuthor{}, core.Target{}, [32]byte{}, syslogwire.SignedBlock{}, ErrAuthentication
	}
	r, err := syslogwire.Parse(raw)
	if err != nil {
		return RouteAuthor{}, core.Target{}, [32]byte{}, syslogwire.SignedBlock{}, ErrAuthentication
	}
	g, ok := a.grants[strings.ToLower(r.Hostname)]
	if !ok || now.Before(g.before) || !now.Before(g.after) {
		return RouteAuthor{}, core.Target{}, [32]byte{}, syslogwire.SignedBlock{}, ErrAuthentication
	}
	b, err := g.key.Verify(raw, covered)
	if err != nil {
		return RouteAuthor{}, core.Target{}, [32]byte{}, syslogwire.SignedBlock{}, ErrAuthentication
	}
	return g.author, g.target, g.signer, b, nil
}

// VerifyPayload accepts a fully assembled C-type payload only when its exact
// DER certificate matches the provisioned origin. Its timestamp is authenticated
// only after every carrying certificate block has independently been verified.
func (a *SignedSyslogAuthenticator) VerifyPayload(hostname string, payload []byte) (time.Time, error) {
	if a == nil || len(payload) == 0 || len(payload) > syslogwire.MaxRecord {
		return time.Time{}, ErrAuthentication
	}
	g, ok := a.grants[strings.ToLower(hostname)]
	if !ok {
		return time.Time{}, ErrAuthentication
	}
	timestamp, rest, ok := strings.Cut(string(payload), " ")
	if !ok || !strings.HasPrefix(rest, "C ") {
		return time.Time{}, ErrAuthentication
	}
	if _, err := syslogwire.Parse([]byte("<110>1 " + timestamp + " host.example app 1 payload -")); err != nil {
		return time.Time{}, ErrAuthentication
	}
	started, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return time.Time{}, ErrAuthentication
	}
	text := rest[2:]
	der, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil || base64.StdEncoding.EncodeToString(der) != text || !bytes.Equal(der, g.certificate) {
		return time.Time{}, ErrAuthentication
	}
	return started, nil
}
