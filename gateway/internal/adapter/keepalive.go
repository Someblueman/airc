package adapter

import (
	"time"

	"github.com/quic-go/quic-go"
)

// Keepalive is a listener's timing for quiet connections (see
// config.Keepalive); a zero field keeps the carrier's default.
type Keepalive struct {
	Idle, Ping time.Duration
}

func (k Keepalive) idle(fallback time.Duration) time.Duration {
	if k.Idle > 0 {
		return k.Idle
	}
	return fallback
}

func (k Keepalive) ping(fallback time.Duration) time.Duration {
	if k.Ping > 0 {
		return k.Ping
	}
	return fallback
}

// QUIC returns config with the listener's idle timeout and PING interval.
// Without a configured interval the server sends no PINGs: the client's own
// keep-alive, or a follow's frames, hold the connection.
func (k Keepalive) QUIC(config *quic.Config) *quic.Config {
	config = config.Clone()
	config.MaxIdleTimeout = k.idle(config.MaxIdleTimeout)
	config.KeepAlivePeriod = k.ping(config.KeepAlivePeriod)
	return config
}
