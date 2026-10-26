package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/dunglas/httpsfv"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	wt "github.com/quic-go/webtransport-go"
)

const webTransportProtocol = "airc-gateway.v1"

type webTransportQUICConnKey struct{}

// WebTransport owns upgraded session handlers. The service assigns certificates
// and owns the UDP socket, QUIC connections, and their serving workers.
type WebTransport struct {
	H3                       *http3.Server
	WT                       *wt.Server
	d                        *dispatch.Dispatcher
	requests, streams, slots chan struct{}
	origins                  map[string]bool
	mu                       sync.Mutex
	closed                   bool
	handlers                 sync.WaitGroup
	done                     chan struct{}
	closeOnce                sync.Once
	closeErr                 error
}

func WebTransportServer(d *dispatch.Dispatcher, requests chan struct{}, origins []string) (*WebTransport, error) {
	if d == nil || cap(requests) < 1 || cap(requests) > config.MaxRequests {
		return nil, store.ErrInvalid
	}
	if err := config.ValidateOrigins(origins); err != nil {
		return nil, err
	}
	a := &WebTransport{d: d, requests: requests, streams: make(chan struct{}, cap(requests)), slots: make(chan struct{}, cap(requests)), origins: make(map[string]bool, len(origins)), done: make(chan struct{})}
	for _, origin := range origins {
		a.origins[origin] = true
	}
	a.H3 = &http3.Server{
		TLSConfig:      &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h3"}},
		QUICConfig:     &quic.Config{Allow0RTT: true, EnableDatagrams: true, EnableStreamResetPartialDelivery: true, HandshakeIdleTimeout: 5 * time.Second, MaxIdleTimeout: 30 * time.Second, MaxIncomingStreams: int64(cap(requests)), MaxIncomingUniStreams: 16, InitialStreamReceiveWindow: 64 << 10, MaxStreamReceiveWindow: 64 << 10, InitialConnectionReceiveWindow: 64 << 10, MaxConnectionReceiveWindow: 64 << 10},
		MaxHeaderBytes: 16 << 10, IdleTimeout: 30 * time.Second,
	}
	a.H3.ConnContext = func(ctx context.Context, conn *quic.Conn) context.Context {
		return context.WithValue(ctx, webTransportQUICConnKey{}, conn)
	}
	a.WT = &wt.Server{H3: a.H3, Config: &wt.Config{MaxIncomingStreams: 4, MaxIncomingUniStreams: 4, MaxIncomingData: 64 << 10}, ApplicationProtocols: []string{webTransportProtocol}, ReorderingTimeout: 2 * time.Second, CheckOrigin: a.checkOrigin}
	a.H3.Handler = http.HandlerFunc(a.handleConnect)
	wt.ConfigureHTTP3Server(a.H3)
	return a, nil
}

func (a *WebTransport) checkOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	return len(values) == 0 || len(values) == 1 && a.origins[values[0]]
}

func webTransportOffered(values []string) bool {
	list, err := httpsfv.UnmarshalList(values)
	if err != nil {
		return false
	}
	found := false
	for _, member := range list {
		item, ok := member.(httpsfv.Item)
		if !ok {
			return false
		}
		value, ok := item.Value.(string)
		if !ok {
			return false
		}
		found = found || value == webTransportProtocol
	}
	return found
}

func (a *WebTransport) handleConnect(w http.ResponseWriter, r *http.Request) {
	control := http.NewResponseController(w)
	if control.SetReadDeadline(time.Now().Add(5*time.Second)) != nil || control.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path != "/v1/webtransport" || r.Method != http.MethodConnect || r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Idempotency-Key")) != 0 || len(r.Header.Values("X-Forwarded-Host")) != 0 || len(r.Header.Values("X-Original-Host")) != 0 || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || !r.TLS.HandshakeComplete || !webTransportOffered(r.Header.Values("WT-Available-Protocols")) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !a.checkOrigin(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	conn, _ := r.Context().Value(webTransportQUICConnKey{}).(*quic.Conn)
	if conn == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	select {
	case a.slots <- struct{}{}:
	default:
		a.mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	a.handlers.Add(1)
	a.mu.Unlock()
	defer a.handlers.Done()
	defer func() { <-a.slots }()
	session, err := a.WT.Upgrade(w, r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if session.SessionState().ApplicationProtocol != webTransportProtocol {
		_ = session.CloseWithError(1, "protocol required")
		return
	}
	// Upgrade takes ownership of the CONNECT stream. Its capsule channel remains
	// alive for the session; the five-second HTTP setup bounds no longer apply.
	if control.SetReadDeadline(time.Time{}) != nil || control.SetWriteDeadline(time.Time{}) != nil {
		_ = session.CloseWithError(1, "deadline setup failed")
		return
	}
	a.serveSession(r.Context(), session, conn)
}

func (a *WebTransport) serveSession(parent context.Context, session *wt.Session, conn *quic.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	sessionDone := make(chan struct{})
	stopSession := context.AfterFunc(session.Context(), func() { cancel(); close(sessionDone) })
	defer func() {
		if !stopSession() {
			<-sessionDone
		}
	}()
	// One author session per WebTransport session, shared by its streams
	// and datagrams; the source is the client's IP address.
	plain := authorIngress(a.d, conn.RemoteAddr(), true)
	defer closeIngress(plain)
	var work sync.WaitGroup
	work.Go(func() {
		select {
		case <-a.done:
			cancel()
		case <-ctx.Done():
		}
	})
	work.Go(func() { _ = serveWebTransportDatagrams(ctx, session, conn, a.d, a.requests, plain); cancel() })
	work.Go(func() { a.acceptBidi(ctx, session, &work, plain) })
	work.Go(func() { a.acceptUni(ctx, session, &work, plain) })
	<-ctx.Done()
	_ = session.CloseWithError(0, "session closed")
	work.Wait()
}

func (a *WebTransport) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		close(a.done)
		a.mu.Unlock()
		a.closeErr = a.WT.Close()
		a.handlers.Wait()
	})
	return a.closeErr
}

func (a *WebTransport) ServeQUICConn(conn *quic.Conn) error {
	if conn == nil {
		return errors.New("nil QUIC connection")
	}
	return a.WT.ServeQUICConn(conn)
}
