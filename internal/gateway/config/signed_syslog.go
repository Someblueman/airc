package config

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/netip"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// SignedSyslog provisions origin authority separately from TLS peer grants.
// CertificateDER is canonical base64 public certificate material, never a key.
type SignedSyslog struct {
	Routes []SignedSyslogRoute `json:"routes"`
}
type SignedSyslogRoute struct {
	CertificateDER string      `json:"certificate_der"`
	Hostname       string      `json:"hostname"`
	Principal      string      `json:"principal"`
	Target         core.Target `json:"target"`
}

func (c Config) validateSignedSyslog(l Listener) error {
	if l.Protocol != "syslog_signed" {
		if l.SignedSyslog != nil {
			return errors.New("signed syslog options require syslog_signed protocol")
		}
		return nil
	}
	if l.SignedSyslog == nil || len(l.SignedSyslog.Routes) < 1 || len(l.SignedSyslog.Routes) > 128 {
		return errors.New("signed syslog requires 1-128 origin grants")
	}
	seen := map[string]bool{}
	for _, r := range l.SignedSyslog.Routes {
		if !SignedSyslogHostname(r.Hostname) || seen[strings.ToLower(r.Hostname)] || !id(r.Principal) {
			return errors.New("invalid or duplicate signed syslog origin")
		}
		seen[strings.ToLower(r.Hostname)] = true
		if _, err := SignedSyslogCertificate(r.CertificateDER); err != nil {
			return err
		}
		allowed := false
		for _, p := range c.Principals {
			if p.ID == r.Principal && !p.Revoked && slices.Contains(p.Operations, core.Send) && slices.Contains(p.Targets, r.Target) {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("signed syslog origin requires an active principal authorized for the exact target")
		}
	}
	return nil
}

func SignedSyslogCertificate(text string) (*x509.Certificate, error) {
	if len(text) == 0 || len(text) > 12000 {
		return nil, errors.New("invalid signed syslog certificate")
	}
	der, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil || base64.StdEncoding.EncodeToString(der) != text || len(der) > 8192 {
		return nil, errors.New("invalid signed syslog certificate")
	}
	c, err := x509.ParseCertificate(der)
	if err != nil || c.PublicKeyAlgorithm != x509.DSA || c.IsCA || len(c.UnhandledCriticalExtensions) != 0 || c.KeyUsage != 0 && c.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !c.NotBefore.Before(c.NotAfter) {
		return nil, errors.New("invalid signed syslog signing certificate")
	}
	return c, nil
}

// Names are literal ASCII FQDNs or IP addresses; no wildcard/header authority.
func SignedSyslogHostname(s string) bool {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Zone() == ""
	}
	if len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range label {
			b := label[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-') {
				return false
			}
		}
	}
	return true
}
