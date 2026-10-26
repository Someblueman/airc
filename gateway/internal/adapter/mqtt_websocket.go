package adapter

import (
	"context"
	"crypto/tls"
	"net/http"
	"slices"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/coder/websocket"
)

// MQTTWebSocketPath is where MQTT over WebSocket connects, the path
// brokers and browser clients default to.
const MQTTWebSocketPath = "/mqtt"

// MQTTWebSocketSubprotocol is the registered subprotocol for MQTT over
// WebSocket, for MQTT 3.1.1 and 5 alike.
const MQTTWebSocketSubprotocol = "mqtt"

// MQTTWebSocketServer serves MQTT over WebSocket: binary messages carry the
// MQTT byte stream, for browsers and networks that pass only HTTP. Its
// connections behave as the mqtt listener's and share one broker, living
// for parent, its MQTT 5 sessions kept in sessionsFile; close joins its
// wills and saves the sessions.
func MQTTWebSocketServer(parent context.Context, d *dispatch.Dispatcher, requests chan struct{}, options config.MQTT, allowedOrigins []string, sessionsFile string) (*http.Server, func(), error) {
	allowedOrigins = slices.Clone(allowedOrigins)
	if err := config.ValidateOrigins(allowedOrigins); err != nil {
		return nil, nil, err
	}
	b, err := newMQTTBroker(parent, d, requests, options, sessionsFile)
	if err != nil {
		return nil, nil, err
	}
	origins := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = true
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != MQTTWebSocketPath || r.Method != http.MethodGet {
			httpFailure(w, http.StatusNotFound, core.InvalidRequest)
			return
		}
		values := r.Header.Values("Origin")
		if len(values) > 1 || len(values) == 1 && !origins[values[0]] {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		if !requestedSubprotocol(r.Header.Values("Sec-WebSocket-Protocol"), MQTTWebSocketSubprotocol) {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:    []string{MQTTWebSocketSubprotocol},
			OriginPatterns:  allowedOrigins,
			CompressionMode: websocket.CompressionDisabled,
		})
		if err != nil {
			return
		}
		// MQTT's own limits bound its packets; the stream may split or
		// join them across messages.
		conn.SetReadLimit(-1)
		b.serve(r.Context(), websocket.NetConn(r.Context(), conn, websocket.MessageBinary))
	})
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(false)
	return &http.Server{
		Handler:           h,
		Protocols:         protocols,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}},
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}, b.close, nil
}
