package config

import (
	"errors"
	"time"
)

// Keepalive tunes how a listener that holds long-lived connections treats a
// quiet one, for links behind NAT or on mobile networks. IdleSeconds is how
// long a connection may carry nothing before it is closed: QUIC's idle
// timeout on http3, webtransport and rawquic, the wait for the next request
// on websocket. PingSeconds is how often the gateway probes the connection
// (WebSocket pings, QUIC PING frames), which keeps NAT mappings open; a
// WebSocket ping unanswered by the next one closes the connection. A zero
// field keeps the carrier's default.
type Keepalive struct {
	IdleSeconds int `json:"idle_seconds,omitempty"`
	PingSeconds int `json:"ping_seconds,omitempty"`
}

// keepaliveProtocols lists the listeners Keepalive applies to.
var keepaliveProtocols = map[string]bool{"websocket": true, "http3": true, "webtransport": true, "rawquic": true, "rawquic_datagrams": true}

func validateKeepalive(l Listener) error {
	k := l.Keepalive
	if k == nil {
		return nil
	}
	if !keepaliveProtocols[l.Protocol] {
		return errors.New("keepalive applies to websocket, http3, webtransport and rawquic listeners")
	}
	if k.IdleSeconds != 0 && (k.IdleSeconds < 5 || k.IdleSeconds > 3600) || k.PingSeconds < 0 || k.PingSeconds > 600 {
		return errors.New("keepalive idle_seconds must be 5-3600 and ping_seconds 1-600")
	}
	if k.IdleSeconds != 0 && k.PingSeconds >= k.IdleSeconds {
		return errors.New("keepalive ping_seconds must be shorter than idle_seconds")
	}
	return nil
}

// KeepaliveTiming is the listener's idle timeout and ping interval; zero
// keeps the carrier's default.
func (l Listener) KeepaliveTiming() (idle, ping time.Duration) {
	if l.Keepalive == nil {
		return 0, 0
	}
	return time.Duration(l.Keepalive.IdleSeconds) * time.Second, time.Duration(l.Keepalive.PingSeconds) * time.Second
}
