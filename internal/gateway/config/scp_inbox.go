package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// SCPInbox accepts plain contract requests uploaded to a forced-command SCP
// account. The SSH login (its UID) is the access gate; an upload posts as the
// author it names, else as BoundAuthor.
type SCPInbox struct {
	LoginUID    uint32       `json:"login_uid"`
	BoundAuthor *BoundAuthor `json:"bound_author,omitempty"`
	Guests      *bool        `json:"guests,omitempty"`
	Target      core.Target  `json:"target"`
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
	if profile == nil || listener.Address != "" || listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
		return errors.New("scp_inbox requires options and cannot use address, TLS or origins")
	}
	if len(listener.Operations) != 1 || listener.Operations[0] != core.Send {
		return errors.New("scp_inbox supports only the send operation")
	}
	if profile.LoginUID == 0 || profile.LoginUID > 1<<31-1 || validRouteAuthor(profile.BoundAuthor, profile.Guests) != nil {
		return errors.New("scp_inbox requires a valid login UID and an author policy")
	}
	target := profile.Target
	if err := target.Normalize(); err != nil || target != profile.Target {
		return errors.New("scp_inbox target must be canonical")
	}
	if !listener.Enabled {
		return nil
	}
	if !Available("scp_inbox") {
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
	if c.EnabledBridges() > 0 {
		return errors.New("enabled scp_inbox cannot accompany an enabled broker, bridge or mailbox owner")
	}
	return nil
}

// EnabledBridges counts the enabled top-level bridges (Redis, IRC, XMPP,
// SFTP and PostgreSQL).
func (c Config) EnabledBridges() int {
	n := 0
	count := func(enabled bool) {
		if enabled {
			n++
		}
	}
	for _, b := range c.RedisPubSub {
		count(b.Enabled)
	}
	for _, b := range c.RedisStreams {
		count(b.Enabled)
	}
	for _, b := range c.IRCBridges {
		count(b.Enabled)
	}
	for _, b := range c.XMPPBridges {
		count(b.Enabled)
	}
	for _, b := range c.SFTP {
		count(b.Enabled)
	}
	for _, b := range c.PostgresNotify {
		count(b.Enabled)
	}
	return n
}
