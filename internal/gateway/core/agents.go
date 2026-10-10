package core

import "github.com/Someblueman/airc/pkg/irc"

const (
	MaxAgents      = 256
	MaxAgentsBytes = 64 << 10
)

type AgentsResult struct {
	Version int             `json:"version"`
	Agents  []irc.AgentInfo `json:"agents"`
}
