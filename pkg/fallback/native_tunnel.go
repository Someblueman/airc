package fallback

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/coder/websocket"
)

// The native tunnel carriers (irc_websocket, irc_connect) carry AIRC's own
// protocol through the gateway, so a connection over them is a native one:
// every command, long-lived sessions and RESUME all work, through port 443
// and HTTP proxies. The client dials the carrier, then hands the stream to
// pkg/irc as it would a TCP connection.

// NativeCarrier reports a carrier that carries native IRC: aircd itself, or
// a gateway tunnel to it.
func NativeCarrier(name string) bool {
	switch name {
	case "irc", "irc_websocket", "irc_connect":
		return true
	}
	return false
}

// ircWebSocketConn is a net.Conn over an IRCv3 WebSocket: each IRC line is
// one message without its CRLF. Reads append the CRLF pkg/irc's scanner
// needs; writes send each complete line as a message.
type ircWebSocketConn struct {
	conn     *websocket.Conn
	raw      net.Conn // the socket underneath, for addresses and deadlines
	kind     websocket.MessageType
	readMu   sync.Mutex
	pending  []byte // the rest of a message a short Read left
	writeMu  sync.Mutex
	partial  []byte // an incomplete line awaiting its newline
	readCtx  context.Context
	cancel   context.CancelFunc
	deadline time.Time
}

func (c *ircWebSocketConn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(c.pending) == 0 {
		ctx := c.readCtx
		if !c.deadline.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, c.deadline)
			defer cancel()
		}
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil && c.readCtx.Err() == nil {
				return 0, &timeoutError{}
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return 0, io.EOF
			}
			return 0, err
		}
		c.pending = append(bytes.TrimRight(data, "\r\n"), '\r', '\n')
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *ircWebSocketConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.partial = append(c.partial, b...)
	for {
		i := bytes.IndexByte(c.partial, '\n')
		if i < 0 {
			return len(b), nil
		}
		line := bytes.TrimRight(c.partial[:i], "\r")
		c.partial = c.partial[i+1:]
		if len(line) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(c.readCtx, 10*time.Second)
		err := c.conn.Write(ctx, c.kind, line)
		cancel()
		if err != nil {
			return 0, err
		}
	}
}

func (c *ircWebSocketConn) Close() error {
	c.cancel()
	err := c.conn.Close(websocket.StatusNormalClosure, "")
	_ = c.conn.CloseNow()
	return err
}

func (c *ircWebSocketConn) LocalAddr() net.Addr  { return c.raw.LocalAddr() }
func (c *ircWebSocketConn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }

func (c *ircWebSocketConn) SetDeadline(t time.Time) error {
	c.readMu.Lock()
	c.deadline = t
	c.readMu.Unlock()
	return nil
}

func (c *ircWebSocketConn) SetReadDeadline(t time.Time) error { return c.SetDeadline(t) }
func (c *ircWebSocketConn) SetWriteDeadline(time.Time) error  { return nil }

// timeoutError is a read deadline passing, as net.Conn reports it.
type timeoutError struct{}

func (*timeoutError) Error() string   { return "i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }

// dialIRCWebSocket opens the gateway's IRC WebSocket tunnel and returns it
// as a net.Conn for pkg/irc.
func (c *Client) dialIRCWebSocket(ctx context.Context, e Endpoint, progress func()) (net.Conn, error) {
	network, address := c.address(e)
	var raw net.Conn
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := c.dialStream(ctx, e, progress)
			if err == nil {
				raw = conn
			}
			return conn, err
		},
		TLSClientConfig:     c.tlsConfig(e),
		TLSHandshakeTimeout: c.connectTimeout(),
	}
	scheme, host := "ws", address
	if e.TLS {
		scheme = "wss"
	}
	if network == "unix" {
		host = "localhost"
	}
	conn, _, err := websocket.Dial(ctx, scheme+"://"+host+"/v1/irc", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{"text.ircv3.net", "binary.ircv3.net"}})
	if err != nil {
		return nil, err
	}
	if raw == nil {
		_ = conn.CloseNow()
		return nil, errors.New("websocket dial returned no connection")
	}
	conn.SetReadLimit(protocol.MaxLineLength + 2)
	kind := websocket.MessageText
	if conn.Subprotocol() == "binary.ircv3.net" {
		kind = websocket.MessageBinary
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &ircWebSocketConn{conn: conn, raw: raw, kind: kind, readCtx: lifetime, cancel: cancel}, nil
}

// dialIRCConnect opens the gateway's HTTP CONNECT tunnel: the stream after
// the 200 is aircd's own protocol.
func (c *Client) dialIRCConnect(ctx context.Context, e Endpoint, progress func()) (net.Conn, error) {
	conn, err := c.dialStream(ctx, e, progress)
	if err != nil {
		return nil, err
	}
	if cfg := c.tlsConfig(e, "http/1.1"); cfg != nil {
		if conn, err = handshake(ctx, conn, cfg); err != nil {
			return nil, err
		}
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.connectTimeout())
	}
	_ = conn.SetDeadline(deadline)
	if _, err := io.WriteString(conn, "CONNECT airc HTTP/1.1\r\nHost: airc\r\n\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("gateway refused the tunnel: %s", strings.TrimSpace(resp.Status))
	}
	_ = conn.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		// Bytes aircd sent on the heels of the 200 are already read.
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

// bufferedConn serves bytes a response reader buffered before the stream.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.reader.Read(p) }
