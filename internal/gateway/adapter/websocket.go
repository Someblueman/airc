package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/coder/websocket"
)

const websocketPath = "/v1/socket"

// WebSocketSubprotocol is an RFC 6455 token. Unlike the framed TLS ALPN,
// WebSocket subprotocol names cannot contain a slash.
const WebSocketSubprotocol = "airc-gateway.v1"

// websocketPingInterval spaces server pings by default; a ping unanswered
// when the next one is due closes the connection. linkedWebSocketPing is
// empty in release builds; see core.LinkedDuration.
var (
	websocketPingInterval = core.LinkedDuration(linkedWebSocketPing, 10*time.Second)
	linkedWebSocketPing   string
)

// WebSocketServer constructs the HTTP/1.1 WebSocket endpoint. The returned
// server owns upgraded handlers through their request contexts: a service
// BaseContext cancellation closes each hijacked connection and waits for its
// requests before the handler returns. keep sets the listener's ping
// interval and how long a connection may wait between requests.
func WebSocketServer(d *dispatch.Dispatcher, requests chan struct{}, allowedOrigins []string, keep Keepalive) (*http.Server, error) {
	if d == nil || cap(requests) < 1 || cap(requests) > config.MaxRequests {
		return nil, store.ErrInvalid
	}
	allowedOrigins = slices.Clone(allowedOrigins)
	if err := config.ValidateOrigins(allowedOrigins); err != nil {
		return nil, err
	}
	origins := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = true
	}
	connections := make(chan struct{}, cap(requests))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != websocketPath || r.Method != http.MethodGet {
			httpFailure(w, http.StatusNotFound, core.InvalidRequest)
			return
		}
		if r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Idempotency-Key")) != 0 {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		values := r.Header.Values("Origin")
		if len(values) > 1 || len(values) == 1 && !origins[values[0]] {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		if !requestedSubprotocol(r.Header.Values("Sec-WebSocket-Protocol"), WebSocketSubprotocol) {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
		select {
		case connections <- struct{}{}:
			defer func() { <-connections }()
		default:
			httpFailure(w, http.StatusTooManyRequests, core.CapacityExceeded)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:    []string{WebSocketSubprotocol},
			OriginPatterns:  allowedOrigins,
			CompressionMode: websocket.CompressionDisabled,
		})
		if err != nil {
			return
		}
		if conn.Subprotocol() != WebSocketSubprotocol {
			_ = conn.Close(websocket.StatusProtocolError, "subprotocol required")
			return
		}
		var plain *dispatch.Ingress
		if d.AuthorAccess() {
			// One session per connection: an identified author lives as
			// long as this socket and is forgotten when it closes.
			plain = &dispatch.Ingress{Source: d.RemoteSource(r.RemoteAddr), Session: dispatch.NewSession()}
			defer plain.Session.Close()
		}
		serveWebSocket(r.Context(), conn, d, requests, plain, keep)
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
	}, nil
}

func serveWebSocket(parent context.Context, conn *websocket.Conn, d *dispatch.Dispatcher, requests chan struct{}, plain *dispatch.Ingress, keep Keepalive) {
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.CloseNow()
		close(closed)
	})
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	conn.SetReadLimit(MaxRequestFrameBytes)
	var writes sync.Mutex
	write := func(response FrameResponse) error {
		body, err := encodeFrame(response)
		if err != nil || len(body) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		writes.Lock()
		defer writes.Unlock()
		writeCtx, finish := context.WithTimeout(ctx, 3*time.Second)
		defer finish()
		err = conn.Write(writeCtx, websocket.MessageText, body)
		if err != nil {
			cancel()
		}
		return err
	}
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		interval := keep.ping(websocketPingInterval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, finish := context.WithTimeout(ctx, interval)
				err := conn.Ping(pingCtx)
				finish()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-pingDone }()
	// The read waiting for the client's next message ends when its timer
	// fires; a change in whether a request is active restarts the timer.
	var idleMu sync.Mutex
	first, active := true, false
	var reading *time.Timer
	idle := func() time.Duration {
		switch {
		case first:
			return 5 * time.Second
		case active:
			return streamIdle
		}
		return keep.idle(ConnectionIdle)
	}
	streaming := func(streaming bool) {
		idleMu.Lock()
		defer idleMu.Unlock()
		if active != streaming {
			active = streaming
			if reading != nil {
				reading.Reset(idle())
			}
		}
	}
	next := func() ([]byte, error) {
		readCtx, finish := context.WithCancel(ctx)
		defer finish()
		idleMu.Lock()
		reading = time.AfterFunc(idle(), finish)
		idleMu.Unlock()
		kind, body, err := conn.Read(readCtx)
		idleMu.Lock()
		reading.Stop()
		reading = nil
		if err == nil {
			first = false
		}
		idleMu.Unlock()
		if err != nil {
			return nil, err
		}
		if kind != websocket.MessageText || len(body) == 0 || len(body) > MaxRequestFrameBytes {
			return nil, errors.New("invalid websocket message")
		}
		return body, nil
	}
	runCorrelated(ctx, correlatedTransport{next: next, write: write, watchModes: []string{"long_poll", "websocket"}, plain: plain, streaming: streaming}, d, requests)
}

func requestedSubprotocol(values []string, wanted string) bool {
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.TrimSpace(token) == wanted {
				return true
			}
		}
	}
	return false
}
