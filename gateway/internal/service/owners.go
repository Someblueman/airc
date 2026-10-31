package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// startup is what an owner may use while starting its listener.
type startup struct {
	ctx, work    context.Context // Run's context; serving's, canceled at shutdown
	c            config.Config
	journal      *store.Store
	slots        chan struct{} // global sessions
	connections  chan struct{} // global open client connections
	requests     chan struct{} // global in-flight requests
	certificates map[string]tls.Certificate
	owned        *handlers
}

// instance is one started listener.
type instance struct {
	address string // reported in the ready record
	serve   func() error
	// stop begins an orderly shutdown; Run stops every instance at once. Nil
	// for pollers, which observe cancellation and must join before their
	// files close.
	stop func(shutdown context.Context)
	// release frees what the instance holds, after it joined or when a later
	// start fails.
	release func()
}

// closing is an instance whose orderly shutdown and release are one close.
func closing(address string, serve func() error, close func()) instance {
	return instance{address: address, serve: serve, stop: func(context.Context) { close() }, release: close}
}

// owner starts one enabled listener.
type owner func(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error)

// owners holds the constructor of every transport serve owns (each Stream
// and Packet descriptor in config), keyed by protocol. Process carriers run
// under their own commands.
var owners = map[string]owner{
	"http":              stream{http: httpServer(plainHTTPServer)}.start,
	"websocket":         stream{http: httpServer(webSocketServer)}.start,
	"grpc":              stream{http: httpServer(grpcServer)}.start,
	"mcp_http":          stream{http: mcpServer}.start,
	"http3":             quicListener(http3Owner),
	"webtransport":      quicListener(webTransportOwner),
	"rawquic":           quicListener(rawQUICOwner(false)),
	"rawquic_datagrams": quicListener(rawQUICOwner(true)),
	"framed":            stream{serve: serveFramed}.start,
	"irc_websocket":     stream{http: httpServer(ircWebSocketServer), native: true}.start,
	"irc_connect":       stream{http: httpServer(ircConnectServer), native: true}.start,
	"unix":              stream{serve: serveFramed}.start,
	"unixpacket":        stream{serve: serveUnixPacket}.start,
	"zeromq":            stream{serve: serveZeroMQ}.start,
	"nng":               stream{serve: serveNNG}.start,
	"mqtt":              stream{alpn: noALPN, serve: serveMQTT}.start,
	"mqtt_websocket":    stream{http: mqttWebSocketServer}.start,
	"udp":               packet(udpOwner),
	"coap_udp":          packet(udpOwner),
	"unixgram":          packet(unixgramListener),
	"directory_mailbox": mailbox(openDirectoryMailbox),
	"append_log":        mailbox(openAppendLog),
}

func udpOwner(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newUDPOwner(l, d, o.slots, o.requests)
}

func unixgramListener(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newUnixgramOwner(l, d, o.slots, o.requests)
}

// packet owns a datagram listener.
func packet(open func(*startup, config.Listener, *dispatch.Dispatcher) (*packetOwner, error)) owner {
	return func(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
		p, err := open(o, l, d)
		if err != nil {
			return instance{}, err
		}
		return closing(p.addr().String(), func() error { return p.serve(o.work) }, func() { _ = p.close() }), nil
	}
}

// quicListener owns a QUIC listener, which requires its certificate.
func quicListener(open func(*startup, config.Listener, *dispatch.Dispatcher, tls.Certificate) (*quicOwner, error)) owner {
	return func(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
		pair, ok := o.certificates[l.ID]
		if !ok {
			return instance{}, errors.New(l.Protocol + " listener requires TLS certificates")
		}
		q, err := open(o, l, d, pair)
		if err != nil {
			return instance{}, err
		}
		if q.server != nil {
			q.server.Handler = o.owned.wrap(q.server.Handler)
		}
		return instance{
			address: q.addr().String(),
			serve:   func() error { return q.serve(o.work) },
			stop:    func(shutdown context.Context) { _ = q.close(shutdown) },
			release: func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = q.close(ctx)
				cancel()
			},
		}, nil
	}
}

func http3Owner(o *startup, l config.Listener, d *dispatch.Dispatcher, pair tls.Certificate) (*quicOwner, error) {
	return newHTTP3Owner(o.work, d, o.connections, o.c.Limits.Requests(), l.AllowedOrigins, pair, l.Address, keepalive(l))
}

func webTransportOwner(o *startup, l config.Listener, d *dispatch.Dispatcher, pair tls.Certificate) (*quicOwner, error) {
	return newWebTransportOwner(o.work, d, o.connections, o.requests, l.AllowedOrigins, pair, l.Address, keepalive(l))
}

func rawQUICOwner(datagrams bool) func(*startup, config.Listener, *dispatch.Dispatcher, tls.Certificate) (*quicOwner, error) {
	return func(o *startup, l config.Listener, d *dispatch.Dispatcher, pair tls.Certificate) (*quicOwner, error) {
		return newRawQUICOwner(o.work, d, o.connections, o.requests, pair, l.Address, datagrams, keepalive(l))
	}
}

// keepalive is the listener's quiet-connection timing.
func keepalive(l config.Listener) adapter.Keepalive {
	idle, ping := l.KeepaliveTiming()
	return adapter.Keepalive{Idle: idle, Ping: ping}
}

// mailbox owns a polled directory: shutdown cancels the poller, and its
// files close only once it has joined.
func mailbox(open func(config.Config, config.Listener, *dispatch.Dispatcher, chan struct{}) (mailboxOwner, error)) owner {
	return func(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
		m, err := open(o.c, l, d, o.requests)
		if err != nil {
			return instance{}, err
		}
		return instance{address: l.Address, serve: func() error { return m.serve(o.work) }, release: func() { _ = m.Close() }}, nil
	}
}

// alpn is the TLS application protocol a connection listener offers.
type alpn uint8

const (
	framedALPN alpn = iota // the framed contract (adapter.FramedALPN)
	noALPN                 // MQTT negotiates none
)

// stream owns a connection listener: TCP (or Unix stream or seqpacket, by
// the transport's network), optionally under TLS, serving each connection
// with serve or an HTTP server.
type stream struct {
	// http builds the HTTP server, and closes the MCP sessions it owns (nil
	// if none).
	http  func(*startup, config.Listener, *dispatch.Dispatcher) (*http.Server, func(), error)
	serve func(context.Context, served) error
	alpn  alpn
	// native listeners tunnel AIRC's protocol and bound their connections
	// as author carriers do, without a dispatcher.
	native bool
}

// served is what a connection listener's serve function uses.
type served struct {
	l        config.Listener
	d        *dispatch.Dispatcher
	listener net.Listener
	requests chan struct{}
	stateDir string
}

func serveFramed(ctx context.Context, s served) error {
	return adapter.ServeFramed(ctx, s.listener, s.d, s.requests)
}

func serveUnixPacket(ctx context.Context, s served) error {
	return adapter.ServeUnixPacket(ctx, s.listener, s.d, s.requests)
}

func serveZeroMQ(ctx context.Context, s served) error {
	return adapter.ServeZeroMQ(ctx, s.listener, s.d, s.requests)
}

func serveNNG(ctx context.Context, s served) error {
	return adapter.ServeNNG(ctx, s.listener, s.d, s.requests)
}

func serveMQTT(ctx context.Context, s served) error {
	return adapter.ServeMQTT(ctx, s.listener, s.d, s.requests, *s.l.MQTT, mqttSessionsFile(s.stateDir, s.l))
}

func mqttWebSocketServer(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, func(), error) {
	return adapter.MQTTWebSocketServer(o.work, d, o.requests, *l.MQTT, l.AllowedOrigins, mqttSessionsFile(o.c.StateDir, l))
}

// mqttSessionsFile is where an MQTT listener keeps its sessions across
// restarts: in the private state directory, by listener ID.
func mqttSessionsFile(stateDir string, l config.Listener) string {
	return filepath.Join(stateDir, "mqtt-"+l.ID+".sessions")
}

func plainHTTPServer(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, error) {
	return adapter.HTTPServer(d, o.c.Limits.Requests(), l.AllowedOrigins)
}

func webSocketServer(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, error) {
	return adapter.WebSocketServer(d, o.requests, l.AllowedOrigins, keepalive(l))
}

// nativeUpstream is the AIRC endpoint a native tunnel relays to, dialled as
// the author sessions dial it, with the gateway credential for WEBIRC.
func nativeUpstream(o *startup, l config.Listener) (adapter.IRCUpstream, error) {
	entry, ok := o.c.AuthorUpstream()
	if !ok {
		return adapter.IRCUpstream{}, errors.New(l.Protocol + " listener requires an upstream")
	}
	u := adapter.IRCUpstream{Network: entry.Network, Address: entry.Address, Listener: l.ID}
	var err error
	if entry.GatewayTokenFile != "" {
		if u.GatewayToken, err = auth.ReadToken(entry.GatewayTokenFile); err != nil {
			return adapter.IRCUpstream{}, auth.ErrCredential
		}
	}
	if entry.TLSServerName != "" {
		u.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ServerName: entry.TLSServerName}
		if entry.TLSCAFile != "" {
			if u.TLS.RootCAs, err = auth.ReadTrustRoots(entry.TLSCAFile); err != nil {
				return adapter.IRCUpstream{}, auth.ErrCredential
			}
		}
	}
	return u, nil
}

func ircWebSocketServer(o *startup, l config.Listener, _ *dispatch.Dispatcher) (*http.Server, error) {
	u, err := nativeUpstream(o, l)
	if err != nil {
		return nil, err
	}
	return adapter.IRCWebSocketServer(u, l.AllowedOrigins)
}

func ircConnectServer(o *startup, l config.Listener, _ *dispatch.Dispatcher) (*http.Server, error) {
	u, err := nativeUpstream(o, l)
	if err != nil {
		return nil, err
	}
	return adapter.IRCConnectServer(u)
}

func grpcServer(o *startup, _ config.Listener, d *dispatch.Dispatcher) (*http.Server, error) {
	return adapter.GRPCServer(d, o.requests)
}

// httpServer adapts an HTTP server constructor that owns nothing else.
func httpServer(open func(*startup, config.Listener, *dispatch.Dispatcher) (*http.Server, error)) func(*startup, config.Listener, *dispatch.Dispatcher) (*http.Server, func(), error) {
	return func(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, func(), error) {
		server, err := open(o, l, d)
		return server, nil, err
	}
}

func mcpServer(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, func(), error) {
	server, mcp, err := adapter.MCPHTTPServer(d, o.requests, o.c.Limits.Sessions, o.c.Limits.SessionsPerAuthor, l.AllowedOrigins)
	if err != nil {
		return nil, nil, err
	}
	return server, mcp.Close, nil
}

// listen binds a connection listener on the transport's network.
func (s stream) listen(ctx context.Context, l config.Listener) (net.Listener, error) {
	t, _ := config.Lookup(l.Protocol)
	switch t.Network {
	case "unix":
		return adapter.ListenUnix(l.Address)
	case "unixpacket":
		return adapter.ListenUnixPacket(l.Address)
	}
	return (&net.ListenConfig{}).Listen(ctx, "tcp", l.Address)
}

func (s stream) start(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
	var server *http.Server
	closeSessions := func() {}
	if s.http != nil {
		var sessions func()
		var err error
		if server, sessions, err = s.http(o, l, d); err != nil {
			return instance{}, err
		}
		if sessions != nil {
			closeSessions = sessions
		}
		server.Handler = o.owned.wrap(server.Handler)
		server.BaseContext = func(net.Listener) context.Context { return o.work }
	}
	listener, err := s.listen(o.ctx, l)
	if err != nil {
		closeSessions()
		return instance{}, err
	}
	if config.AuthorCarrier(l.Protocol) || s.native {
		limits := l.AccessLimits()
		listener = adapter.LimitConnections(listener, limits.Connections, limits.ConnectionsPerSource)
	}
	// Each open client connection takes one of the global connection slots;
	// the requests it carries take upstream sessions only while they run.
	var transport net.Listener = limitedListener{Listener: listener, slots: o.connections}
	if pair, ok := o.certificates[l.ID]; ok {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, NextProtos: []string{adapter.FramedALPN}}
		if s.alpn == noALPN {
			tlsConfig.NextProtos = nil
		}
		if server != nil {
			tlsConfig = server.TLSConfig.Clone()
			tlsConfig.Certificates = []tls.Certificate{pair}
		}
		transport = tls.NewListener(transport, tlsConfig)
	}
	serve := func() error {
		return s.serve(o.work, served{l: l, d: d, listener: transport, requests: o.requests, stateDir: o.c.StateDir})
	}
	if server != nil {
		serve = func() error { return server.Serve(transport) }
	}
	return instance{
		address: listener.Addr().String(),
		serve:   serve,
		stop: func(shutdown context.Context) {
			closeSessions()
			if server != nil {
				_ = server.Shutdown(shutdown)
				_ = server.Close()
			} else {
				_ = transport.Close()
			}
		},
		release: func() {
			closeSessions()
			if server != nil {
				_ = server.Close()
			}
			_ = transport.Close()
		},
	}, nil
}
