package core

import "time"

const MaxPollBytes = 16 << 10

type PollResult struct {
	Target    Target    `json:"target"`
	ID        string    `json:"id"`
	From      string    `json:"from"`
	AccountID string    `json:"account_id,omitempty"`
	Question  string    `json:"question"`
	Options   []string  `json:"options"`
	Votes     []int     `json:"votes"`
	ExpiresAt time.Time `json:"expires_at"`
	Closed    bool      `json:"closed"`
}
