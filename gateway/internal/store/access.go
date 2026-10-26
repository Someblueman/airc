package store

import (
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
)

// Access contains only caller-visible permission/limit snapshots. Credential
// paths, upstream details and other authors never enter this result.
type Access struct {
	Operations []core.Operation `json:"operations"`
	Targets    []core.Target    `json:"targets"`
	Limits     config.Limits    `json:"limits"`
	Retry      config.Retry     `json:"retry_defaults"`
}

// Defaults is the caller-visible limit and retry snapshot shared by authors.
func (s *Store) Defaults() (config.Limits, config.Retry) {
	return s.config.Limits, s.config.Retry
}
