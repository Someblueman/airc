package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpHTTPBodyLimit = 32 << 10

// MCPHTTP owns admission state and the public SDK session lifecycle for one
// Streamable HTTP endpoint. Authorization and bounded gateway session
// admission happen before handing a request to the SDK.
type MCPHTTP struct {
	mu          sync.Mutex
	closed      bool
	global      int
	per         map[string]int
	sessions    map[string]string
	max         int
	maxPer      int
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	closedCh    chan struct{}
	servers     map[*mcp.Server]struct{}
	sessionsSDK map[*mcp.ServerSession]*mcp.Server
	authors     map[*mcp.ServerSession]*mcpAuthorIngress
	sessionWG   sync.WaitGroup
}

// MCPHTTPServer constructs a Streamable HTTP MCP endpoint without binding a
// listener. The caller owns Serve and the returned http.Server lifecycle.
func MCPHTTPServer(d *dispatch.Dispatcher, v *auth.Verifier, requests chan struct{}, sessions, perPrincipal int, origins []string) (*http.Server, *MCPHTTP, error) {
	if d == nil || v == nil || cap(requests) < 1 || sessions < 1 || sessions > 256 || perPrincipal < 1 || perPrincipal > sessions {
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
	m := &MCPHTTP{per: map[string]int{}, sessions: map[string]string{}, max: sessions, maxPer: perPrincipal, cancel: cancel, closedCh: make(chan struct{}), servers: map[*mcp.Server]struct{}{}, sessionsSDK: map[*mcp.ServerSession]*mcp.Server{}, authors: map[*mcp.ServerSession]*mcpAuthorIngress{}}
	sdkHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		initial, _ := r.Context().Value(mcpInitializationKey{}).(*mcpInitialization)
		if initial == nil {
			return nil
		}
		var s *mcp.Server
		if initial.author != nil {
			initial.author.Session = dispatch.NewSession()
			s = newMCPAuthorServer(lifetime, d, initial.author.Ingress)
		} else {
			s = newMCPServer(lifetime, d, bearerToken(r))
		}
		if !m.trackServer(s) {
			initial.author.close()
			return nil
		}
		initial.server = s
		return s
	}, &mcp.StreamableHTTPOptions{SessionTimeout: statefulSessionTimeout})
	sdkAuthorized := sdkauth.RequireBearerToken(func(_ context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		identity, err := v.Authenticate(token)
		if err != nil {
			return nil, sdkauth.ErrInvalidToken
		}
		// The SDK requires an expiration on this per-request authentication
		// assertion. Gateway credential validity still comes from the verifier
		// and durable permissions on every HTTP request, not this timestamp.
		return &sdkauth.TokenInfo{UserID: identity.ID() + "\x00" + identity.Binding(), Expiration: time.Now().Add(25 * time.Second)}, nil
	}, nil)(sdkHandler)
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
		// A plain author session needs no TLS, as on IRC; bearer principals
		// keep TLS or loopback.
		plain := len(r.Header.Values("Authorization")) == 0 && d.AuthorAccess()
		if !plain && r.TLS == nil && !netLoopback(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
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
		var owner string
		var author *mcpAuthorIngress
		if plain {
			in := dispatch.Ingress{Source: d.RemoteSource(r.RemoteAddr)}
			if _, err := headerSubmission(r, &in); err != nil || len(r.Header.Values(headerNick)) != 0 {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			owner, author = "source:"+in.Source, &mcpAuthorIngress{Ingress: in}
		} else {
			token, ok := strictBearer(r)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			identity, err := v.Authenticate(token)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if _, err := d.Capabilities(r.Context(), token); err != nil {
				status := http.StatusForbidden
				if err == auth.ErrAuthentication {
					status = http.StatusUnauthorized
				}
				http.Error(w, http.StatusText(status), status)
				return
			}
			owner = identity.ID()
		}
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
		initial := &mcpInitialization{author: author}
		if reserved {
			r = r.WithContext(context.WithValue(r.Context(), mcpInitializationKey{}, initial))
		}
		if plain {
			sdkHandler.ServeHTTP(w, r)
		} else {
			sdkAuthorized.ServeHTTP(w, r)
		}
		if reserved {
			m.finishInitialization(owner, initial.server, initial.author)
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
