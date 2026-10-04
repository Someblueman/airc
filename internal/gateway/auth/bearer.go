// Package auth resolves provisioned gateway credentials to immutable principal
// permissions. It does not grant dispatch, persist revocation, or contact AIRC.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

var (
	ErrAuthentication = errors.New(string(core.AuthenticationFailed))
	ErrForbidden      = errors.New(string(core.Forbidden))
	ErrCredential     = errors.New("credential_file_invalid")
	ErrPlatform       = errors.New("credential filesystem verification unavailable on this platform")
)

type principal struct {
	id, binding string
	operations  []core.Operation
	targets     []core.Target
	revoked     bool
	verifier    [32]byte
	bearer      bool
}

// Identity can only be populated by successful authentication. Its permission
// snapshot is immutable; future dispatch must separately check durable current
// authorization and obtain a bounded grant ordered against revocation.
type Identity struct{ principal *principal }

func (i Identity) ID() string {
	if i.principal == nil {
		return ""
	}
	return i.principal.id
}
func (i Identity) Binding() string {
	if i.principal == nil {
		return ""
	}
	return i.principal.binding
}
func (i Identity) Operations() []core.Operation {
	if i.principal == nil {
		return nil
	}
	return slices.Clone(i.principal.operations)
}

func (i Identity) Targets() []core.Target {
	if i.principal == nil {
		return nil
	}
	return slices.Clone(i.principal.targets)
}

type Verifier struct {
	principals []principal
	profiles   messageProfiles
}

// New snapshots validated configuration and stores only token hashes. Rotation
// through a new snapshot preserves principal/binding identity, not credential IDs.
func New(c config.Config) (*Verifier, error) {
	c.Principals = slices.Clone(c.Principals)
	for j := range c.Principals {
		c.Principals[j].Targets = slices.Clone(c.Principals[j].Targets)
		c.Principals[j].Operations = slices.Clone(c.Principals[j].Operations)
		c.Principals[j].SigningKeys = slices.Clone(c.Principals[j].SigningKeys)
	}
	c.Listeners = slices.Clone(c.Listeners)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := c.CheckFiles(); err != nil {
		return nil, err
	}
	v := &Verifier{principals: make([]principal, 0, len(c.Principals))}
	seen := map[[32]byte]bool{}
	for _, p := range c.Principals {
		if p.BearerTokenFile == "" {
			v.principals = append(v.principals, principal{id: p.ID, binding: p.Binding, operations: p.Operations, targets: p.Targets, revoked: p.Revoked})
			continue
		}
		token, err := ReadToken(p.BearerTokenFile)
		if err != nil {
			return nil, fmt.Errorf("principal %s: %w", p.ID, err)
		}
		material, _ := hex.DecodeString(token)
		hash := sha256.Sum256(material)
		clear(material)
		if seen[hash] {
			return nil, errors.New("gateway credentials must be unique across principals")
		}
		seen[hash] = true
		v.principals = append(v.principals, principal{id: p.ID, binding: p.Binding, operations: p.Operations, targets: p.Targets, revoked: p.Revoked, verifier: hash, bearer: true})
	}
	if err := v.loadProfiles(c); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *Verifier) Authenticate(token string) (Identity, error) {
	if v == nil || len(token) != 64 {
		return Identity{}, ErrAuthentication
	}
	material, err := hex.DecodeString(token)
	if err != nil {
		return Identity{}, ErrAuthentication
	}
	hash := sha256.Sum256(material)
	clear(material)
	var identity Identity
	// Always compare every provisioned verifier: the matching entry's position
	// must not control how many constant-time secret comparisons are performed.
	for j := range v.principals {
		p := &v.principals[j]
		if subtle.ConstantTimeCompare(hash[:], p.verifier[:]) == 1 && p.bearer && !p.revoked {
			identity.principal = p
		}
	}
	if identity.principal == nil {
		return Identity{}, ErrAuthentication
	}
	return identity, nil
}

// Authorize checks normalized intent against exact configured permissions.
// Receipt retrieval still needs a principal-scoped store key; replies still need
// retained-parent authorization, and reads need returned-record filtering.
func (i Identity) Authorize(r core.Request) error {
	if i.principal == nil || i.principal.revoked {
		return ErrForbidden
	}
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	if err := r.Normalize(); err != nil {
		return ErrForbidden
	}
	if !slices.Contains(i.principal.operations, r.Operation) {
		return ErrForbidden
	}
	if r.Target != nil && !slices.Contains(i.principal.targets, *r.Target) {
		return ErrForbidden
	}
	return nil
}
