package core

import "github.com/Someblueman/airc/pkg/irc"

const (
	MaxWaitingSignals = 1024
	MaxWaitingBytes   = 64 << 10
)

type WaitingResult struct {
	Target   Target          `json:"target"`
	ParentID string          `json:"parent_id"`
	Signals  []irc.ChatEntry `json:"signals"`
}
