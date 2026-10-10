package config

import (
	"errors"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// FTPSInbox maps a private mailbox tree to one signed AIRC principal.
type FTPSInbox struct {
	LoginUID               uint32      `json:"login_uid"`
	Principal              string      `json:"principal"`
	Target                 core.Target `json:"target"`
	RecipientKeyID         string      `json:"recipient_key_id"`
	RecipientPublicKeyFile string      `json:"recipient_public_key_file"`
}

func FTPSInboxAvailable() bool { return runtime.GOOS == "linux" }

func (c Config) validateFTPSInbox() error {
	count := 0
	listeners := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "ftps_inbox" {
			if listener.FTPSInbox != nil {
				return errors.New("ftps_inbox options require an ftps_inbox listener")
			}
			continue
		}
		count++
		if count > 16 {
			return errors.New("FTPS inbox listener count limit exceeded")
		}
		profile := listener.FTPSInbox
		if profile == nil || !absolute(listener.Address) || filepath.Clean(listener.Address) != listener.Address || len(listener.Address) > 4096 {
			return errors.New("ftps_inbox requires options and a canonical absolute mailbox root")
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("ftps_inbox cannot use listener TLS, origins or anonymous access")
		}
		if profile.LoginUID == 0 || profile.LoginUID > 1<<31-1 || !id(profile.Principal) || !id(profile.RecipientKeyID) || !absolute(profile.RecipientPublicKeyFile) {
			return errors.New("ftps_inbox requires a valid login UID, principal and response recipient key")
		}
		if len(listener.Operations) == 0 || operations(listener.Operations) != nil || len(c.RecipientKeys) == 0 || c.ResponseSigningKey == nil {
			return errors.New("ftps_inbox requires explicit operations, encrypted ingress keys and a response signer")
		}
		for _, op := range listener.Operations {
			if op != core.Send && op != core.ContextRead && op != core.HistoryRead && op != core.Search {
				return errors.New("ftps_inbox supports only send, context.read, history.read and search")
			}
		}
		target := profile.Target
		if err := target.Normalize(); err != nil || target != profile.Target {
			return errors.New("ftps_inbox target must be canonical")
		}
		principalFound := false
		for _, principal := range c.Principals {
			if principal.ID != profile.Principal {
				continue
			}
			if principal.Revoked || len(principal.SigningKeys) == 0 {
				return errors.New("ftps_inbox principal must be active and signed")
			}
			for _, op := range listener.Operations {
				if !slices.Contains(principal.Operations, op) {
					return errors.New("ftps_inbox principal lacks an allowed operation grant")
				}
			}
			for _, granted := range principal.Targets {
				if err := granted.Normalize(); err == nil && granted == target {
					principalFound = true
					break
				}
			}
			break
		}
		if !principalFound {
			return errors.New("ftps_inbox principal must grant the configured target")
		}
		if listener.Enabled && !FTPSInboxAvailable() {
			return errors.New("enabled ftps_inbox requires Linux")
		}
		listeners[listener.ID] = true
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "ftps_inbox" || listeners[route.Listener] {
			return errors.New("ftps_inbox uses its configured protected result key and cannot use return routes")
		}
	}
	return nil
}
