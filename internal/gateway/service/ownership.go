package service

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/core"
	"net"
	"net/http"
	"sync"
)

type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func (l limitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConn{Conn: conn, slots: l.slots}, nil
		default:
			_ = conn.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	slots chan struct{}
	once  sync.Once
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { <-c.slots })
	return err
}

type handlers struct {
	mu     sync.Mutex
	closed bool
	work   sync.WaitGroup
}

func (h *handlers) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(struct {
				Version int          `json:"version"`
				Error   core.Failure `json:"error"`
			}{1, core.Failure{Code: core.UpstreamUnavailable, Transient: true}})
			return
		}
		h.work.Add(1)
		h.mu.Unlock()
		defer h.work.Done()
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) stop() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
}
