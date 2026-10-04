package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
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

// WebSocketServer constructs the HTTP/1.1 WebSocket endpoint. The returned
// server owns upgraded handlers through their request contexts: a service
// BaseContext cancellation closes each hijacked connection and waits for its
// requests before the handler returns.
func WebSocketServer(d *dispatch.Dispatcher, requests chan struct{}, allowedOrigins []string) (*http.Server, error) {
	if d == nil || cap(requests) < 1 || cap(requests) > 256 {
		return nil, store.ErrInvalid
	}
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
		if r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Idempotency-Key")) != 0 {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		values := r.Header.Values("Origin")
		if len(values) > 1 || len(values) == 1 && !origins[values[0]] {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		if !requestedSubprotocol(r.Header.Values("Sec-WebSocket-Protocol"), FramedALPN) {
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
			Subprotocols:    []string{FramedALPN},
			OriginPatterns:  allowedOrigins,
			CompressionMode: websocket.CompressionDisabled,
		})
		if err != nil {
			return
		}
		if conn.Subprotocol() != FramedALPN {
			_ = conn.Close(websocket.StatusProtocolError, "subprotocol required")
			return
		}
		serveWebSocket(r.Context(), conn, d, requests, r.TLS == nil)
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

func serveWebSocket(parent context.Context, conn *websocket.Conn, d *dispatch.Dispatcher, requests chan struct{}, encryptedOnly bool) {
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
		body, err := json.Marshal(response)
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
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, finish := context.WithTimeout(ctx, 3*time.Second)
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
	first := true
	next := func() ([]byte, error) {
		idle := 30 * time.Second
		if first {
			idle = 5 * time.Second
		}
		readCtx, finish := context.WithTimeout(ctx, idle)
		kind, body, err := conn.Read(readCtx)
		finish()
		if err != nil {
			return nil, err
		}
		first = false
		if kind != websocket.MessageText || len(body) == 0 || len(body) > MaxRequestFrameBytes {
			return nil, errors.New("invalid websocket message")
		}
		return body, nil
	}
	runCorrelated(ctx, correlatedTransport{next: next, write: write, watchModes: []string{"long_poll", "websocket"}}, d, requests, encryptedOnly)
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
