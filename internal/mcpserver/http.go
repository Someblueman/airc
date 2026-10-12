package mcpserver

import (
	"context"
	"crypto/rand"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// SessionHooks follow one Streamable HTTP session. Opened runs once the
// session's initialize has succeeded, before its session ID reaches the
// client. Closed runs exactly once, after the session has ended and its calls
// have returned; opened reports whether Opened ran.
type SessionHooks struct {
	Opened func(id string)
	Closed func(id string, opened bool)
}

// HTTPHandler serves MCP's Streamable HTTP transport with stateful sessions
// and no stream resumption: each POSTed call is answered on its own
// text/event-stream response, notifications are accepted with 202, a GET holds
// an idle server stream open and DELETE ends a session. A call runs in its
// session's context, not the request's, so a client that disconnects does not
// cancel it; notifications/cancelled does.
type HTTPHandler struct {
	// NewSession supplies the server for a POST without a session ID. It may
	// return nil to refuse the session.
	NewSession func(*http.Request) (*Server, SessionHooks)
	// SessionTimeout closes a session this long after its last POST ends.
	SessionTimeout time.Duration

	mu       sync.Mutex
	closed   bool
	sessions map[string]*httpSession
}

type httpSession struct {
	id      string
	handler *HTTPHandler
	session *Session
	hooks   SessionHooks
	ctx     context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	closing bool
	opened  bool
	posts   int
	stream  bool // a GET stream is open
	timer   *time.Timer
	work    sync.WaitGroup
	done    chan struct{}
}

// Sessions reports the number of live sessions.
func (h *HTTPHandler) Sessions() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

// Close ends every session and refuses new ones. It returns once their calls
// have returned and their Closed hooks have run.
func (h *HTTPHandler) Close() {
	h.mu.Lock()
	h.closed = true
	sessions := make([]*httpSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// DNS rebinding protection, as the MCP SDK applies it: a loopback listener
	// only answers loopback Host names.
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && local != nil {
		if isLoopback(local.String()) && !isLoopback(r.Host) {
			http.Error(w, "Forbidden: invalid Host header "+quote(r.Host), http.StatusForbidden)
			return
		}
	}
	if r.Method == http.MethodPost {
		if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
			http.Error(w, "Content-Type must be 'application/json'", http.StatusUnsupportedMediaType)
			return
		}
	}
	jsonOK, streamOK := accepts(r.Header.Values("Accept"))
	if r.Method == http.MethodGet {
		if !streamOK {
			http.Error(w, "Accept must contain 'text/event-stream' for GET requests", http.StatusBadRequest)
			return
		}
	} else if r.Method != http.MethodDelete && (!jsonOK || !streamOK) {
		http.Error(w, "Accept must contain both 'application/json' and 'text/event-stream'", http.StatusBadRequest)
		return
	}
	id := r.Header.Get("Mcp-Session-Id")
	var s *httpSession
	if id != "" {
		h.mu.Lock()
		s = h.sessions[id]
		h.mu.Unlock()
		if s == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
	}
	switch r.Method {
	case http.MethodDelete:
		if s == nil {
			http.Error(w, "Bad Request: DELETE requires an Mcp-Session-Id header", http.StatusBadRequest)
			return
		}
		s.close()
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodGet:
		if s == nil {
			http.Error(w, "Bad Request: GET requires an Mcp-Session-Id header", http.StatusBadRequest)
			return
		}
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if version := r.Header.Get("Mcp-Protocol-Version"); version != "" && !slices.Contains(supportedVersions, version) {
		http.Error(w, "Bad Request: Unsupported protocol version (supported versions: "+strings.Join(supportedVersions, ",")+")", http.StatusBadRequest)
		return
	}
	created := false
	if s == nil {
		var server *Server
		var hooks SessionHooks
		if h.NewSession != nil {
			server, hooks = h.NewSession(r)
		}
		if server == nil {
			http.Error(w, "no server available", http.StatusBadRequest)
			return
		}
		if s = h.open(server, hooks); s == nil {
			http.Error(w, "server closed", http.StatusServiceUnavailable)
			return
		}
		created = true
	}
	if r.Method == http.MethodGet {
		s.serveGET(w, r)
		return
	}
	if !s.beginPOST() {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	s.servePOST(w, r)
	s.endPOST()
	if created && !s.session.Initialized() {
		s.close() // initialization failed
	}
}

func (h *HTTPHandler) open(server *Server, hooks SessionHooks) *httpSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &httpSession{id: rand.Text(), handler: h, session: server.NewSession(), hooks: hooks, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancel()
		if hooks.Closed != nil {
			hooks.Closed(s.id, false)
		}
		return nil
	}
	if h.sessions == nil {
		h.sessions = map[string]*httpSession{}
	}
	h.sessions[s.id] = s
	if h.SessionTimeout > 0 {
		s.timer = time.AfterFunc(h.SessionTimeout, s.close)
	}
	h.mu.Unlock()
	return s
}

// beginPOST pauses the idle timer and joins the POST to the session's work.
func (s *httpSession) beginPOST() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.work.Add(1)
	if s.posts == 0 && s.timer != nil {
		s.timer.Stop()
	}
	s.posts++
	return true
}

func (s *httpSession) endPOST() {
	s.mu.Lock()
	s.posts--
	if s.posts == 0 && s.timer != nil && !s.closing {
		s.timer.Reset(s.handler.SessionTimeout)
	}
	s.mu.Unlock()
	s.work.Done()
}

// close ends the session: it cancels its calls, waits for its requests, and
// runs the Closed hook. It is idempotent.
func (s *httpSession) close() {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		<-s.done
		return
	}
	s.closing = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	s.handler.mu.Lock()
	delete(s.handler.sessions, s.id)
	s.handler.mu.Unlock()
	s.cancel()
	s.work.Wait()
	s.mu.Lock()
	opened := s.opened
	s.mu.Unlock()
	if s.hooks.Closed != nil {
		s.hooks.Closed(s.id, opened)
	}
	close(s.done)
}

func (s *httpSession) servePOST(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Last-Event-Id")) > 0 {
		http.Error(w, "can't send Last-Event-ID for POST request", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		http.Error(w, "POST requires a non-empty body", http.StatusBadRequest)
		return
	}
	m, err := ParseMessage(body)
	if err != nil {
		http.Error(w, "malformed payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := m.Check(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !m.IsCall() {
		s.session.Handle(s.ctx, m, nil)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	if m.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", s.id)
	}
	// A failed write ends delivery on this stream but not the call.
	var mu sync.Mutex
	var broken error
	write := func(data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if broken != nil {
			return broken
		}
		if broken = writeEvent(w, data); broken != nil {
			return broken
		}
		return nil
	}
	notify := func(_ context.Context, data []byte) error { return write(data) }
	response := s.session.Handle(s.ctx, m, notify)
	if m.Method == "initialize" && s.session.Initialized() {
		s.mu.Lock()
		first := !s.opened
		s.opened = true
		s.mu.Unlock()
		if first && s.hooks.Opened != nil {
			s.hooks.Opened(s.id)
		}
	}
	if response != nil {
		_ = write(response)
	}
}

func (s *httpSession) serveGET(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Last-Event-Id")) > 0 {
		http.Error(w, "stream replay unsupported", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.closing || s.stream {
		s.mu.Unlock()
		http.Error(w, "stream ID conflicts with ongoing stream", http.StatusConflict)
		return
	}
	s.stream = true
	s.work.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.stream = false
		s.mu.Unlock()
		s.work.Done()
	}()
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_ = http.NewResponseController(w).Flush()
	// This server sends nothing unprompted; hold the stream until either side ends.
	select {
	case <-r.Context().Done():
	case <-s.ctx.Done():
	}
}

func writeEvent(w http.ResponseWriter, data []byte) error {
	event := make([]byte, 0, len(data)+24)
	event = append(event, "event: message\ndata: "...)
	event = append(event, data...)
	event = append(event, "\n\n"...)
	if _, err := w.Write(event); err != nil {
		return err
	}
	_ = http.NewResponseController(w).Flush()
	return nil
}

func accepts(values []string) (jsonOK, streamOK bool) {
	for _, value := range values {
		for raw := range strings.SplitSeq(value, ",") {
			base, _, _ := strings.Cut(strings.TrimSpace(raw), ";")
			switch strings.ToLower(strings.TrimSpace(base)) {
			case "application/json", "application/*":
				jsonOK = true
			case "text/event-stream", "text/*":
				streamOK = true
			case "*/*":
				jsonOK, streamOK = true, true
			}
		}
	}
	return jsonOK, streamOK
}

func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = strings.Trim(address, "[]")
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
