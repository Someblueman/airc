package store

import (
	"context"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// Access contains only caller-visible permission/limit snapshots. Credential
// paths, bindings, account names and other principals never enter this result.
type Access struct {
	Operations []core.Operation `json:"operations"`
	Targets    []core.Target    `json:"targets"`
	Limits     config.Limits    `json:"limits"`
	Retry      config.Retry     `json:"retry_defaults"`
}

// Permissions orders discovery against durable revocation without allocating
// upstream work, touching jobs, or replenishing any attempt budget.
func (s *Store) Permissions(ctx context.Context, identity auth.Identity) (Access, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Access{}, ErrClosed
	}
	for _, p := range s.config.Principals {
		if p.ID != identity.ID() || p.Binding != identity.Binding() || p.Revoked {
			continue
		}
		var revoked bool
		if err := s.db.QueryRowContext(ctx, "SELECT revoked FROM authorization WHERE principal=? AND binding=?", p.ID, p.Binding).Scan(&revoked); err != nil {
			return Access{}, err
		}
		if revoked {
			return Access{}, auth.ErrForbidden
		}
		access := Access{Operations: core.EffectiveOperations(p.Operations, p.Operations, identity.Operations(), p.Operations), Targets: []core.Target{}, Limits: s.config.Limits, Retry: s.config.Retry}
		allowed := identity.Targets()
		for _, target := range p.Targets {
			if slices.Contains(allowed, target) {
				access.Targets = append(access.Targets, target)
			}
		}
		return access, nil
	}
	return Access{}, auth.ErrForbidden
}
