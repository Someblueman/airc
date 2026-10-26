package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/coder/websocket"
)

// The native tunnel carriers carry AIRC's own protocol, byte for byte,
// between a client and the upstream: IRC lines in WebSocket messages
// (irc_websocket, the IRCv3 WebSocket convention web clients and bouncers
// speak) or through an HTTP CONNECT tunnel (irc_connect, which any IRC
// client with a proxy setting can use). Nothing on the native feature set
// is lost: direct messages, threads, admin commands and long-lived sessions
// all work through port 443 and HTTP proxies. The gateway asserts the
// client's source with WEBIRC first, so AIRC applies its per-address
// limits to the real client; AIRC then admits the client itself (PASS,
// nickname, account). Durability is native's: these connections are not
// journaled.

// IRCUpstream is the AIRC endpoint a native tunnel relays to, dialled as
// the author sessions dial it.
type IRCUpstream struct {
	Network, Address string
	// TLS, when set, is the client configuration for a TLS upstream.
	TLS *tls.Config
	// GatewayToken, when set, is sent with WEBIRC and the client's source
	// before any client byte.
	GatewayToken string
	// Listener is the listener ID, the source label of a client without an
	// address.
	Listener string
}

// The subprotocols of the IRCv3 WebSocket convention: one IRC line per
// message, without its trailing CRLF. A client that offers neither gets
// text messages.
const (
	IRCWebSocketText   = "text.ircv3.net"
	IRCWebSocketBinary = "binary.ircv3.net"
)

// ircWebSocketPaths are the request paths the WebSocket tunnel answers:
// the root, which web clients are usually given, and a versioned one.
var ircWebSocketPaths = []string{"/", "/v1/irc"}

const ircUpstreamDialTimeout = 5 * time.Second

// source is the client's source for WEBIRC: its IP address, or the
// listener's label.
func (u IRCUpstream) source(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		return ip.String()
	}
	return store.ListenerSource(u.Listener)
}

// dial connects to the upstream and asserts the client's source, so the
// connection is ready for the client's first line.
func (u IRCUpstream) dial(ctx context.Context, remote string) (net.Conn, error) {
	if u.Network != "unix" && u.Network != "tcp" || u.Address == "" {
		return nil, errors.New("invalid native upstream")
	}
	ctx, cancel := context.WithTimeout(ctx, ircUpstreamDialTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, u.Network, u.Address)
	if err != nil {
		return nil, err
	}
	if u.TLS != nil {
		secure := tls.Client(conn, u.TLS)
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = secure
	}
	if u.GatewayToken != "" {
		source := u.source(remote)
		if !protocol.ValidSource(source) {
			_ = conn.Close()
			return nil, errors.New("invalid client source")
		}
		_ = conn.SetWriteDeadline(time.Now().Add(ircUpstreamDialTimeout))
		if _, err := io.WriteString(conn, "WEBIRC "+source+" :"+u.GatewayToken+"\r\n"); err != nil {
			_ = conn.Close()
			return nil, err
		}
		_ = conn.SetWriteDeadline(time.Time{})
	}
	return conn, nil
}

// IRCWebSocketServer constructs the HTTP/1.1 server of the WebSocket
// tunnel. Each text or binary message is one IRC line; a trailing CRLF is
// tolerated and never sent. The server's BaseContext ends every tunnel.
func IRCWebSocketServer(u IRCUpstream, allowedOrigins []string) (*http.Server, error) {
	allowedOrigins = slices.Clone(allowedOrigins)
	if err := config.ValidateOrigins(allowedOrigins); err != nil {
		return nil, err
	}
	origins := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = true
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(ircWebSocketPaths, r.URL.Path) || r.Method != http.MethodGet {
			httpFailure(w, http.StatusNotFound, core.InvalidRequest)
			return
		}
		values := r.Header.Values("Origin")
		if len(values) > 1 || len(values) == 1 && !origins[values[0]] {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:    []string{IRCWebSocketText, IRCWebSocketBinary},
			OriginPatterns:  allowedOrigins,
			CompressionMode: websocket.CompressionDisabled,
		})
		if err != nil {
			return
		}
		serveIRCWebSocket(r.Context(), conn, u, r.RemoteAddr)
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

func serveIRCWebSocket(parent context.Context, conn *websocket.Conn, u IRCUpstream, remote string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	conn.SetReadLimit(protocol.MaxLineLength + 2)
	upstream, err := u.dial(ctx, remote)
	if err != nil {
		_ = conn.Close(websocket.StatusTryAgainLater, "upstream unavailable")
		return
	}
	kind := websocket.MessageText
	if conn.Subprotocol() == IRCWebSocketBinary {
		kind = websocket.MessageBinary
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = upstream.Close()
		_ = conn.CloseNow()
		close(closed)
	})
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
	}()
	var relays sync.WaitGroup
	relays.Go(func() {
		// Client lines to the upstream, each with its CRLF restored.
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			line := strings.TrimRight(string(data), "\r\n")
			if line == "" || len(line) > protocol.MaxLineLength || strings.ContainsAny(line, "\r\n\x00") {
				_ = conn.Close(websocket.StatusPolicyViolation, "one IRC line per message")
				return
			}
			_ = upstream.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(upstream, line+"\r\n"); err != nil {
				return
			}
		}
	})
	relays.Go(func() {
		// Upstream lines to the client, one message each, without CRLF.
		defer cancel()
		scanner := bufio.NewScanner(upstream)
		scanner.Buffer(make([]byte, 1024), protocol.MaxLineLength+2)
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r")
			outKind := kind
			if outKind == websocket.MessageText && !utf8.ValidString(line) {
				outKind = websocket.MessageBinary
			}
			writeCtx, finish := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(writeCtx, outKind, []byte(line))
			finish()
			if err != nil {
				return
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
	relays.Wait()
}

// IRCConnectServer constructs the HTTP CONNECT tunnel: a CONNECT request,
// whatever authority it names, is answered 200 and then relays bytes to
// the upstream, after WEBIRC. HTTP/1.1 tunnels hijack the connection;
// HTTP/2 (under TLS, one stream per connection) streams the request body
// and response. The server's BaseContext ends every tunnel.
func IRCConnectServer(u IRCUpstream) (*http.Server, error) {
	if u.Address == "" {
		return nil, store.ErrInvalid
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.Header().Set("Allow", http.MethodConnect)
			httpFailure(w, http.StatusMethodNotAllowed, core.InvalidRequest)
			return
		}
		if r.ProtoMajor >= 2 {
			serveIRCConnectStream(r.Context(), w, r, u)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			httpFailure(w, http.StatusInternalServerError, core.UpstreamUnavailable)
			return
		}
		ctx := r.Context()
		upstream, err := u.dial(ctx, r.RemoteAddr)
		if err != nil {
			httpFailure(w, http.StatusBadGateway, core.UpstreamUnavailable)
			return
		}
		client, buffered, err := hijacker.Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_ = client.SetDeadline(time.Time{})
		_ = client.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			_ = client.Close()
			_ = upstream.Close()
			return
		}
		_ = client.SetWriteDeadline(time.Time{})
		// Lines the client pipelined behind its CONNECT are already read.
		var reader io.Reader = client
		if n := buffered.Reader.Buffered(); n > 0 {
			reader = io.MultiReader(io.LimitReader(buffered.Reader, int64(n)), client)
		}
		relayBytes(ctx, reader, client, upstream)
	})
	return &http.Server{
		Handler:   h,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}},
		// One tunnel per HTTP/2 connection, as over HTTP/1.1, so the
		// listener's connection limits bound the upstream connections.
		HTTP2:             &http.HTTP2Config{MaxConcurrentStreams: 1},
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}, nil
}

// serveIRCConnectStream relays an HTTP/2 CONNECT stream: the request body
// carries the client's bytes and the response the upstream's.
func serveIRCConnectStream(ctx context.Context, w http.ResponseWriter, r *http.Request, u IRCUpstream) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpFailure(w, http.StatusInternalServerError, core.UpstreamUnavailable)
		return
	}
	upstream, err := u.dial(ctx, r.RemoteAddr)
	if err != nil {
		httpFailure(w, http.StatusBadGateway, core.UpstreamUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	relayBytes(ctx, r.Body, &flushWriter{w: w, flusher: flusher, body: r.Body}, upstream)
}

// flushWriter writes a response as a stream: each write is flushed to the
// client. Close ends the request body, which ends the client's reads.
type flushWriter struct {
	w       io.Writer
	flusher http.Flusher
	body    io.Closer
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		f.flusher.Flush()
	}
	return n, err
}

func (f *flushWriter) Close() error { return f.body.Close() }

// relayBytes copies both ways between a client (read from reader, written
// and closed through client) and the upstream until either side ends or
// ctx does, then closes both and waits for both copies.
func relayBytes(ctx context.Context, reader io.Reader, client io.WriteCloser, upstream net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = upstream.Close()
		_ = client.Close()
		close(closed)
	})
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
	}()
	var copies sync.WaitGroup
	copies.Go(func() {
		defer cancel()
		_, _ = io.Copy(upstream, reader)
	})
	copies.Go(func() {
		defer cancel()
		_, _ = io.Copy(client, upstream)
	})
	copies.Wait()
}
