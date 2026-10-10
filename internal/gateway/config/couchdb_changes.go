package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/couchdbwire"
)

// CouchDBChanges provisions one fixed, read-only CouchDB _changes source.
type CouchDBChanges struct {
	ID              string              `json:"id"`
	Enabled         bool                `json:"enabled"`
	Endpoint        string              `json:"endpoint"`
	Database        string              `json:"database"`
	Namespace       string              `json:"namespace"`
	Username        string              `json:"username"`
	PasswordFile    string              `json:"password_file"`
	TLSCAFile       string              `json:"tls_ca_file,omitempty"`
	InitialSequence string              `json:"initial_sequence,omitempty"`
	Route           CouchDBChangesRoute `json:"route"`
}

type CouchDBChangesRoute struct {
	Principal string      `json:"principal"`
	Target    core.Target `json:"target"`
}

// NormalizedInitialSequence returns the configured starting sequence, with
// the default represented canonically as "0" for scope hashing and storage.
func (b CouchDBChanges) NormalizedInitialSequence() string {
	if b.InitialSequence == "" {
		return "0"
	}
	return b.InitialSequence
}

// CouchDBChangesScope pins durable progress to this exact source and route.
// Credential paths and the username are intentionally excluded.
func CouchDBChangesScope(realm, binding string, b CouchDBChanges) string {
	parts := []string{
		"airc-couchdb-changes-v1", realm, b.Namespace, b.ID, b.Endpoint,
		b.Database, b.Route.Principal, binding, string(b.Route.Target.Kind),
		b.Route.Target.Name, b.NormalizedInitialSequence(),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func couchDBUsername(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (c Config) validateCouchDBChanges() error {
	if len(c.CouchDBChanges) > 16 {
		return errors.New("CouchDB changes owner count limit exceeded")
	}
	ids := make(map[string]bool, len(c.CouchDBChanges))
	locations := make(map[string]bool, len(c.CouchDBChanges))
	namespaces := make(map[string]bool, len(c.CouchDBChanges))
	for _, owner := range c.CouchDBChanges {
		if !id(owner.ID) || ids[owner.ID] || couchdbwire.ValidateLocation(owner.Endpoint, owner.Database) != nil ||
			!couchdbwire.ValidNamespace(owner.Namespace) || namespaces[owner.Namespace] || !couchDBUsername(owner.Username) ||
			!absolute(owner.PasswordFile) || owner.TLSCAFile != "" && !absolute(owner.TLSCAFile) ||
			!couchdbwire.ValidSequence(owner.NormalizedInitialSequence()) {
			return errors.New("invalid or duplicate CouchDB changes source")
		}
		ids[owner.ID] = true
		namespaces[owner.Namespace] = true
		location := owner.Endpoint + "\x00" + owner.Database
		if locations[location] {
			return errors.New("duplicate CouchDB changes endpoint and database")
		}
		locations[location] = true
		if owner.Enabled {
			for _, listener := range c.Listeners {
				if listener.Enabled && slices.Contains([]string{"offline_bundle", "file_mailbox", "stdio", "socketpair", "inherited", "fifo", "uart"}, listener.Protocol) {
					return errors.New("CouchDB changes cannot accompany an exclusive command-owned listener")
				}
			}
		}
		if len(c.RecipientKeys) == 0 || !id(owner.Route.Principal) {
			return errors.New("CouchDB changes requires ingress recipient keys and a fixed principal route")
		}
		target := owner.Route.Target
		if err := target.Normalize(); err != nil || target != owner.Route.Target {
			return errors.New("CouchDB changes route target must be canonical")
		}
		principalFound := false
		for _, principal := range c.Principals {
			if principal.ID != owner.Route.Principal || principal.Revoked || len(principal.SigningKeys) == 0 {
				continue
			}
			grantedTarget := false
			for _, granted := range principal.Targets {
				if granted.Normalize() == nil && granted == target {
					grantedTarget = true
					break
				}
			}
			if grantedTarget && slices.Contains(principal.Operations, core.Send) {
				principalFound = true
				break
			}
		}
		if !principalFound {
			return errors.New("CouchDB changes requires an active signing Send principal with the exact target grant")
		}
	}
	return nil
}
