package config

import (
	"errors"
	"fmt"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/pgnotifywire"
)

// PostgresNotify provisions one mTLS-authenticated LISTEN/NOTIFY source. The
// database role allowed to NOTIFY on the channel is its access gate; each
// payload posts as the author it names, else as the route's bound author.
type PostgresNotify struct {
	ID             string              `json:"id"`
	Enabled        bool                `json:"enabled"`
	Address        string              `json:"address"`
	TLSServerName  string              `json:"tls_server_name"`
	TLSCAFile      string              `json:"tls_ca_file,omitempty"`
	ClientCertFile string              `json:"client_cert_file"`
	ClientKeyFile  string              `json:"client_key_file"`
	Database       string              `json:"database"`
	Username       string              `json:"username"`
	Channel        string              `json:"channel"`
	Route          PostgresNotifyRoute `json:"route"`
}

type PostgresNotifyRoute struct {
	BoundAuthor *BoundAuthor `json:"bound_author,omitempty"`
	Guests      *bool        `json:"guests,omitempty"`
	Target      core.Target  `json:"target"`
}

func (c Config) validatePostgresNotify() error {
	if len(c.PostgresNotify) > 16 {
		return errors.New("PostgreSQL notification owner count limit exceeded")
	}
	ids := make(map[string]bool, len(c.PostgresNotify))
	locations := make(map[string]bool, len(c.PostgresNotify))
	for _, owner := range c.PostgresNotify {
		if !id(owner.ID) || ids[owner.ID] ||
			pgnotifywire.ValidateLocation(owner.Address, owner.TLSServerName, owner.Database, owner.Username, owner.Channel) != nil ||
			!absolute(owner.ClientCertFile) || !absolute(owner.ClientKeyFile) || owner.TLSCAFile != "" && !absolute(owner.TLSCAFile) {
			return errors.New("invalid or duplicate PostgreSQL notification source")
		}
		ids[owner.ID] = true
		location := owner.Address + "\x00" + owner.Database + "\x00" + owner.Channel
		if locations[location] {
			return errors.New("duplicate PostgreSQL notification address, database and channel")
		}
		locations[location] = true
		if owner.Enabled {
			for _, listener := range c.Listeners {
				if listener.Enabled && slices.Contains([]string{"offline_bundle", "file_mailbox", "stdio", "socketpair", "inherited", "fifo", "uart"}, listener.Protocol) {
					return errors.New("PostgreSQL notifications cannot accompany an exclusive command-owned listener")
				}
			}
		}
		if err := validRouteAuthor(owner.Route.BoundAuthor, owner.Route.Guests); err != nil {
			return fmt.Errorf("PostgreSQL notifications: %w", err)
		}
		target := owner.Route.Target
		if err := target.Normalize(); err != nil || target != owner.Route.Target {
			return errors.New("PostgreSQL notification target must be canonical")
		}
	}
	return nil
}
