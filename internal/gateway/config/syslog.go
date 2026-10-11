package config

import (
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// Syslog maps an exact end-entity certificate to one bound author and target.
// Record headers are never used as identity or routing authority.
type Syslog struct {
	Routes []SyslogRoute `json:"routes"`
}
type SyslogRoute struct {
	CertificateSHA256 string      `json:"certificate_sha256"`
	BoundAuthor       BoundAuthor `json:"bound_author"`
	Target            core.Target `json:"target"`
}

func (c Config) validateSyslog(l Listener) error {
	if l.Syslog == nil || len(l.Syslog.Routes) < 1 || len(l.Syslog.Routes) > 128 || l.TLSCertFile == "" || len(l.AllowedOrigins) != 0 || len(l.Operations) != 1 || l.Operations[0] != core.Send {
		return errors.New("TLS syslog requires TLS, send-only operations and 1-128 peer grants without origins")
	}
	seen := map[string]bool{}
	for _, r := range l.Syslog.Routes {
		digest, err := hex.DecodeString(r.CertificateSHA256)
		if err != nil || len(digest) != 32 || r.CertificateSHA256 != strings.ToLower(r.CertificateSHA256) || seen[r.CertificateSHA256] || !protocolNick(r.BoundAuthor) {
			return errors.New("invalid or duplicate syslog certificate grant")
		}
		seen[r.CertificateSHA256] = true
		target := r.Target
		if target.Normalize() != nil || target != r.Target {
			return errors.New("syslog grant target must be canonical")
		}
	}
	return nil
}
