package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/mcpserver"
)

const mcpHTTPBodyLimit = 32 << 10

// MCPHTTP owns admission state and the MCP session lifecycle for one
// Streamable HTTP endpoint. Bounded gateway session admission happens before
// handing a request to the MCP handler; each tool call names its author.
type MCPHTTP struct {
	mu       sync.Mutex
	closed   bool
	global   int
	per      map[string]int
	sessions map[string]string
	max      int
	maxPer   int
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closedCh chan struct{}
	handler  *mcpserver.HTTPHandler
}

// MCPHTTPServer constructs a Streamable HTTP MCP endpoint without binding a
// listener. The caller owns Serve and the returned http.Server lifecycle.
func MCPHTTPServer(d *dispatch.Dispatcher, requests chan struct{}, sessions, perSource int, origins []string) (*http.Server, *MCPHTTP, error) {
	if d == nil || !d.AuthorAccess() || cap(requests) < 1 || sessions < 1 || sessions > 256 || perSource < 1 || perSource > sessions {
		return nil, nil, store.ErrInvalid
	}
	if err := config.ValidateOrigins(origins); err != nil {
		return nil, nil, err
	}
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[origin] = true
	}
	lifetime, cancel := context.WithCancel(context.Background())
	m := &MCPHTTP{per: map[string]int{}, sessions: map[string]string{}, max: sessions, maxPer: perSource, cancel: cancel, closedCh: make(chan struct{})}
	m.handler = &mcpserver.HTTPHandler{SessionTimeout: statefulSessionTimeout, NewSession: func(r *http.Request) (*mcpserver.Server, mcpserver.SessionHooks) {
		initial, _ := r.Context().Value(mcpInitializationKey{}).(*mcpInitialization)
		if initial == nil {
			return nil, mcpserver.SessionHooks{}
		}
		initial.started = true
		author, owner := initial.author, initial.owner
		author.Session = dispatch.NewSession()
		return newMCPAuthorServer(lifetime, d, author.Ingress), mcpserver.SessionHooks{
			Opened: func(id string) { m.openSession(owner, id) },
			Closed: func(id string, opened bool) {
				// The session's identified author is forgotten with it.
				author.close()
				if !opened {
					id = ""
				}
				m.releaseSession(owner, id)
			},
		}
	}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.URL.RawQuery != "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id, Mcp-Protocol-Version")
		if !mcpOrigin(w, r, allowed) {
			return
		}
		if r.Method == http.MethodOptions {
			mcpPreflight(w, r, allowed)
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if len(r.Header.Values("Mcp-Session-Id")) > 1 || len(r.Header.Values("Mcp-Protocol-Version")) > 1 || len(r.Header.Values("Last-Event-Id")) > 1 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if !m.acquireRequest(requests) {
			http.Error(w, "capacity exceeded", http.StatusTooManyRequests)
			return
		}
		defer func() { <-requests }()
		if !m.beginRequest() {
			http.Error(w, "server closed", http.StatusServiceUnavailable)
			return
		}
		defer m.wg.Done()
		ctx, stop := context.WithTimeout(r.Context(), 25*time.Second)
		defer stop()
		joined := make(chan struct{})
		stopLifetime := context.AfterFunc(lifetime, func() { stop(); close(joined) })
		defer func() {
			if !stopLifetime() {
				<-joined
			}
		}()
		// An MCP session needs no TLS and no credential, as on IRC.
		controller := http.NewResponseController(w)
		if r.Method == http.MethodPost {
			_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
		}
		_ = controller.SetWriteDeadline(time.Now().Add(25 * time.Second))
		ioJoined := make(chan struct{})
		stopIO := context.AfterFunc(ctx, func() {
			_ = controller.SetReadDeadline(time.Now())
			_ = controller.SetWriteDeadline(time.Now())
			close(ioJoined)
		})
		defer func() {
			if !stopIO() {
				<-ioJoined
			}
		}()
		r = r.WithContext(ctx)
		in := dispatch.Ingress{Source: d.RemoteSource(r.RemoteAddr)}
		if _, err := headerSubmission(r, &in); err != nil || len(r.Header.Values(headerNick)) != 0 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		owner, author := "source:"+in.Source, &mcpAuthorIngress{Ingress: in}
		initialize := false
		if r.Method == http.MethodPost {
			body, isInitialize, err := mcpBody(r)
			_ = controller.SetReadDeadline(time.Time{})
			if err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			initialize = isInitialize
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID != "" {
			m.mu.Lock()
			holder := m.sessions[sessionID]
			m.mu.Unlock()
			if holder != "" && holder != owner {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		} else if !initialize {
			http.Error(w, "session required", http.StatusBadRequest)
			return
		}
		reserved := false
		if sessionID == "" && initialize {
			if !m.acquireSession(owner) {
				http.Error(w, "capacity exceeded", http.StatusTooManyRequests)
				return
			}
			reserved = true
		}
		if m.isClosed() {
			if reserved {
				m.releaseSession(owner, "")
			}
			http.Error(w, "server closed", http.StatusServiceUnavailable)
			return
		}
		initial := &mcpInitialization{author: author, owner: owner}
		if reserved {
			r = r.WithContext(context.WithValue(r.Context(), mcpInitializationKey{}, initial))
		}
		m.handler.ServeHTTP(w, r)
		if reserved && !initial.started {
			// Refused before a session began; the session hooks release the rest.
			m.releaseSession(owner, "")
		}
	})
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	return &http.Server{
		Handler:           handler,
		Protocols:         protocols,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}},
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}, m, nil
}
