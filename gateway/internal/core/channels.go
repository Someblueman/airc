package core

import "time"

const (
	MaxChannels      = 256
	MaxChannelsBytes = 48 << 10
)

// Channel describes one room known from live membership, retained history or a topic.
type Channel struct {
	Name         string    `json:"name"`
	Members      int       `json:"members"`
	Messages     int       `json:"messages"`
	LastActivity time.Time `json:"last_activity,omitzero"`
	Topic        string    `json:"topic,omitempty"`
}

// ChannelsResult is a complete global native CHANNELS snapshot, never truncated.
type ChannelsResult struct {
	Channels []Channel `json:"channels"`
}
