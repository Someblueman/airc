package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// SCPInbox identifies the one signed principal allowed to submit uploads from
// a forced-command legacy SCP account.
type SCPInbox struct {
	LoginUID  uint32      `json:"login_uid"`
	Principal string      `json:"principal"`
	Target    core.Target `json:"target"`
}

func (c Config) validateSCPInbox() error {
	var selected *Listener
	for i := range c.Listeners {
		listener := &c.Listeners[i]
		if listener.Protocol != "scp_inbox" {
			if listener.SCPInbox != nil {
				return errors.New("scp_inbox options require an scp_inbox listener")
			}
			continue
		}
		if selected != nil {
			return errors.New("scp_inbox supports exactly one listener")
		}
		selected = listener
	}
	if selected == nil {
		return nil
	}
	listener := *selected
	profile := listener.SCPInbox
	if profile == nil || listener.Address != "" || listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
		return errors.New("scp_inbox requires options and cannot use address, TLS, origins or anonymous access")
	}
	if len(listener.Operations) != 1 || listener.Operations[0] != core.Send {
		return errors.New("scp_inbox supports only the send operation")
	}
	if profile.LoginUID == 0 || profile.LoginUID > 1<<31-1 || !id(profile.Principal) || len(c.RecipientKeys) == 0 {
		return errors.New("scp_inbox requires a valid login UID, signed principal and encrypted ingress key")
	}
	target := profile.Target
	if err := target.Normalize(); err != nil || target != profile.Target {
		return errors.New("scp_inbox target must be canonical")
	}
	principalFound := false
	for _, principal := range c.Principals {
		if principal.ID != profile.Principal {
			continue
		}
		if principal.Revoked || len(principal.SigningKeys) == 0 || !slices.Contains(principal.Operations, core.Send) {
			return errors.New("scp_inbox principal must be active, signed and granted send")
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
		return errors.New("scp_inbox principal must be active and granted the configured target")
	}
	if !listener.Enabled {
		return nil
	}
	if !StdioAvailable() {
		return errors.New("scp_inbox requires Darwin or Linux")
	}
	enabledListeners := 0
	for _, candidate := range c.Listeners {
		if candidate.Enabled {
			enabledListeners++
		}
	}
	if enabledListeners != 1 {
		return errors.New("enabled scp_inbox must be the sole enabled listener")
	}
	if c.hasEnabledBrokerOrMailboxOwner() {
		return errors.New("enabled scp_inbox cannot accompany an enabled broker, bridge or mailbox owner")
	}
	return nil
}

func (c Config) hasEnabledBrokerOrMailboxOwner() bool {
	for _, owner := range c.RedisPubSub {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.RedisStreams {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.IRCBridges {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.Kafka {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.SQS {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.XMPPBridges {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.MatrixBridges {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.SFTP {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.S3 {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.SQLiteMailboxes {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.CouchDBChanges {
		if owner.Enabled {
			return true
		}
	}
	for _, owner := range c.PostgresNotify {
		if owner.Enabled {
			return true
		}
	}
	return false
}
