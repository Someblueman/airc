package config

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// Syslog maps an exact end-entity certificate to one existing sender and target.
// Record headers are never used as identity or routing authority.
type Syslog struct {
	Routes []SyslogRoute `json:"routes"`
}
type SyslogRoute struct {
	CertificateSHA256 string      `json:"certificate_sha256"`
	Principal         string      `json:"principal"`
	Target            core.Target `json:"target"`
}

func (c Config) validateSyslog(l Listener) error {
	if l.Protocol != "syslog_tls" && l.Protocol != "syslog_signed" {
		if l.Syslog != nil {
			return errors.New("syslog options require a TLS syslog protocol")
		}
		return nil
	}
	if l.Syslog == nil || len(l.Syslog.Routes) < 1 || len(l.Syslog.Routes) > 128 || l.TLSCertFile == "" || len(l.AllowedOrigins) != 0 || len(l.Operations) != 1 || l.Operations[0] != core.Send {
		return errors.New("TLS syslog requires TLS, send-only operations and 1-128 peer grants without origins")
	}
	seen := map[string]bool{}
	for _, r := range l.Syslog.Routes {
		digest, err := hex.DecodeString(r.CertificateSHA256)
		if err != nil || len(digest) != 32 || r.CertificateSHA256 != strings.ToLower(r.CertificateSHA256) || seen[r.CertificateSHA256] || !id(r.Principal) {
			return errors.New("invalid or duplicate syslog certificate grant")
		}
		seen[r.CertificateSHA256] = true
		allowed := false
		for _, p := range c.Principals {
			if p.ID == r.Principal && !p.Revoked && slices.Contains(p.Operations, core.Send) && slices.Contains(p.Targets, r.Target) {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("syslog peer requires an active principal authorized for the exact target")
		}
	}
	return nil
}
