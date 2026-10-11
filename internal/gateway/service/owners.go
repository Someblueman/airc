package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// startup is what an owner may use while starting its listener.
type startup struct {
	ctx, work    context.Context // Run's context; serving's, canceled at shutdown
	c            config.Config
	journal      *store.Store
	slots        chan struct{} // global sessions
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
// under their own commands; bridges are in bridgeOwners.
var owners = map[string]owner{
	"http":              stream{http: httpServer(plainHTTPServer)}.start,
	"websocket":         stream{http: httpServer(webSocketServer)}.start,
	"grpc":              stream{http: httpServer(grpcServer)}.start,
	"mcp_http":          stream{http: mcpServer}.start,
	"webrtc":            stream{http: webRTCServer}.start,
	"http3":             quicListener(http3Owner),
	"webtransport":      quicListener(webTransportOwner),
	"rawquic":           quicListener(rawQUICOwner(false)),
	"rawquic_datagrams": quicListener(rawQUICOwner(true)),
	"framed":            stream{serve: serveFramed}.start,
	"unix":              stream{serve: serveFramed}.start,
	"unixpacket":        stream{serve: serveUnixPacket}.start,
	"sctp":              stream{sctp: adapter.SCTPOrdered, serve: serveSCTP}.start,
	"sctp_unordered":    stream{sctp: adapter.SCTPUnordered, serve: serveSCTP}.start,
	"sctp_pr_ttl":       stream{sctp: adapter.SCTPOrderedTTL, serve: serveSCTP}.start,
	"zeromq":            stream{serve: serveZeroMQ}.start,
	"nng":               stream{serve: serveNNG}.start,
	"coap_tcp":          stream{serve: serveCoAPTCP}.start,
	"coaps_tcp":         stream{alpn: coapALPN, serve: serveCoAPTCP}.start,
	"mqtt":              stream{alpn: noALPN, serve: serveMQTT}.start,
	"amqp":              stream{alpn: noALPN, selfBounded: true, serve: serveAMQP}.start,
	"stomp":             stream{alpn: noALPN, selfBounded: true, serve: serveSTOMP}.start,
	"syslog_tls":        stream{selfBounded: true, serve: serveSyslog}.start,
	"syslog_signed":     stream{selfBounded: true, serve: serveSyslog}.start,
	"dtls":              startDTLS,
	"coaps":             startDTLS,
	"udp":               packet(udpOwner),
	"udp_lite":          packet(udpOwner),
	"ipv4_multicast":    packet(udpOwner),
	"ipv4_broadcast":    packet(udpOwner),
	"ipv6_multicast":    packet(udpOwner),
	"coap_udp":          packet(udpOwner),
	"icmp_v6":           packet(icmpV6Owner),
	"unixgram":          packet(unixgramListener),
	"system_v_queue":    packet(systemVQueueOwner),
	"posix_mqueue":      packet(posixQueueOwner),
	"shared_memory":     packet(sharedMemoryOwner),
	"mapped_file":       packet(mappedFileOwner),
	"snmp_v3":           startSNMP,
	"mqtt_sn":           startMQTTSN,
	"mach_ipc":          startMachIPC,
	"coap_oscore":       startOSCOREListener,
	"directory_mailbox": mailbox(openDirectoryMailbox),
	"append_log":        mailbox(openAppendLog),
}

func udpOwner(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newUDPOwner(l, d, o.slots, o.requests)
}

func icmpV6Owner(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newICMPv6Owner(l, d, o.slots, o.requests)
}

func unixgramListener(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newUnixgramOwner(l, d, o.slots, o.requests)
}

func systemVQueueOwner(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newSystemVQueueOwner(o.work, o.c, l, o.journal, d, o.slots, o.requests)
}

func posixQueueOwner(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newPOSIXQueueOwner(o.work, o.c, l, o.journal, d, o.slots, o.requests)
}

func sharedMemoryOwner(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newSharedMemoryOwner(o.work, o.c, l, d, o.slots, o.requests)
}

func mappedFileOwner(o *startup, l config.Listener, d *dispatch.Dispatcher) (*packetOwner, error) {
	return newMappedFileOwner(o.work, o.c, l, d, o.slots, o.requests)
}

// packet owns a datagram, queue or shared-memory listener.
func packet(open func(*startup, config.Listener, *dispatch.Dispatcher) (*packetOwner, error)) owner {
	return func(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
		p, err := open(o, l, d)
		if err != nil {
			return instance{}, err
		}
		return closing(p.addr().String(), func() error { return p.serve(o.work) }, func() { _ = p.close() }), nil
	}
}

func startDTLS(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
	owner, err := newDTLSOwner(d, o.slots, o.requests, o.certificates[l.ID], l.Address, l.Protocol)
	if err != nil {
		return instance{}, err
	}
	return closing(owner.addr().String(), func() error { return owner.serve(o.work) }, func() { _ = owner.close() }), nil
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
	return newHTTP3Owner(o.work, d, o.slots, l.AllowedOrigins, pair, l.Address)
}

func webTransportOwner(o *startup, l config.Listener, d *dispatch.Dispatcher, pair tls.Certificate) (*quicOwner, error) {
	return newWebTransportOwner(o.work, d, o.slots, o.requests, l.AllowedOrigins, pair, l.Address)
}

func rawQUICOwner(datagrams bool) func(*startup, config.Listener, *dispatch.Dispatcher, tls.Certificate) (*quicOwner, error) {
	return func(o *startup, l config.Listener, d *dispatch.Dispatcher, pair tls.Certificate) (*quicOwner, error) {
		return newRawQUICOwner(o.work, d, o.slots, o.requests, pair, l.Address, datagrams)
	}
}

func startSNMP(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
	a, err := auth.NewSNMPAuthenticator(*l.SNMP)
	if err != nil {
		return instance{}, err
	}
	return udpInstance(l, func(conn net.PacketConn) error { return adapter.ServeSNMP(o.work, conn, a, d, o.requests) })
}

func startMQTTSN(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
	return udpInstance(l, func(conn net.PacketConn) error { return adapter.ServeMQTTSN(o.work, conn, d, o.requests, *l.MQTTSN) })
}

// udpInstance binds a plain UDP socket served by serve.
func udpInstance(l config.Listener, serve func(net.PacketConn) error) (instance, error) {
	conn, err := net.ListenPacket("udp", l.Address)
	if err != nil {
		return instance{}, err
	}
	return closing(conn.LocalAddr().String(), func() error { return serve(conn) }, func() { _ = conn.Close() }), nil
}

func startMachIPC(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
	owner, err := newMachIPCOwner(o.ctx, l, d, o.requests)
	if err != nil {
		return instance{}, err
	}
	return closing(l.Address, func() error { return owner.serve(o.work) }, func() { _ = owner.close() }), nil
}

func startOSCOREListener(o *startup, l config.Listener, d *dispatch.Dispatcher) (instance, error) {
	owner, err := startOSCORE(o.work, o.c, l, o.journal, d, o.requests)
	if err != nil {
		return instance{}, err
	}
	return closing(owner.Addr().String(), owner.Wait, func() { _ = owner.Close() }), nil
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
	noALPN                 // MQTT, AMQP and STOMP negotiate none
	coapALPN               // CoAP over TLS
)

// stream owns a connection listener: TCP (or Unix stream, seqpacket or
// SCTP, by the transport's network), optionally under TLS, serving each
// connection with serve or an HTTP server.
type stream struct {
	// http builds the HTTP server, and closes the MCP or WebRTC sessions it
	// owns (nil if none).
	http  func(*startup, config.Listener, *dispatch.Dispatcher) (*http.Server, func(), error)
	serve func(context.Context, served) error
	alpn  alpn
	// selfBounded listeners take global session slots themselves.
	selfBounded bool
	sctp        adapter.SCTPMode
}

// served is what a connection listener's serve function uses.
type served struct {
	l                 config.Listener
	d                 *dispatch.Dispatcher
	listener          net.Listener
	slots, requests   chan struct{}
	sessionsPerAuthor int
	realm             string
	syslog            *auth.SyslogAuthenticator
	signedSyslog      *auth.SignedSyslogAuthenticator
	sctp              adapter.SCTPMode
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
	return adapter.ServeMQTT(ctx, s.listener, s.d, s.requests, *s.l.MQTT)
}

func serveAMQP(ctx context.Context, s served) error {
	return adapter.ServeAMQP(ctx, s.listener, s.slots, *s.l.AMQP, s.d, s.requests)
}

func serveSTOMP(ctx context.Context, s served) error {
	return adapter.ServeSTOMP(ctx, s.listener, s.slots, *s.l.STOMP, s.d, s.requests)
}

func serveSCTP(ctx context.Context, s served) error {
	return adapter.ServeSCTP(ctx, s.listener, s.d, s.requests, s.sctp)
}

func serveCoAPTCP(ctx context.Context, s served) error {
	return serveCoAPTCPListener(ctx, s.listener, s.d, s.requests)
}

func serveSyslog(ctx context.Context, s served) error {
	return adapter.ServeSyslog(ctx, s.listener, s.syslog, s.signedSyslog, s.slots, s.sessionsPerAuthor, s.realm, s.d, s.requests)
}

func plainHTTPServer(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, error) {
	return adapter.HTTPServer(d, o.c.Limits.Sessions, l.AllowedOrigins)
}

func webSocketServer(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, error) {
	return adapter.WebSocketServer(d, o.requests, l.AllowedOrigins)
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

func webRTCServer(o *startup, l config.Listener, d *dispatch.Dispatcher) (*http.Server, func(), error) {
	options := config.WebRTC{}
	if l.WebRTC != nil {
		options = *l.WebRTC
	}
	if options.ICETransportPolicy == "" {
		options.ICETransportPolicy = "all"
	}
	server, webrtc, err := adapter.WebRTCServer(d, o.slots, o.requests, o.c.Limits.SessionsPerAuthor, options, l.AllowedOrigins)
	if err != nil {
		return nil, nil, err
	}
	if server.TLSConfig == nil {
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	return server, func() { _ = webrtc.Close() }, nil
}

// listen binds a connection listener on the transport's network.
func (s stream) listen(ctx context.Context, l config.Listener) (net.Listener, error) {
	t, _ := config.Lookup(l.Protocol)
	switch t.Network {
	case "sctp":
		return adapter.ListenSCTP(l.Address, s.sctp)
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
	var syslog *auth.SyslogAuthenticator
	if l.Syslog != nil {
		if syslog, err = auth.NewSyslogAuthenticator(*l.Syslog); err != nil {
			_ = listener.Close()
			return instance{}, err
		}
	}
	if config.AuthorCarrier(l.Protocol) {
		limits := l.AccessLimits()
		listener = adapter.LimitConnections(listener, limits.Connections, limits.ConnectionsPerSource)
	}
	var transport net.Listener = limitedListener{Listener: listener, slots: o.slots}
	var signedSyslog *auth.SignedSyslogAuthenticator
	if l.SignedSyslog != nil {
		if signedSyslog, err = auth.NewSignedSyslogAuthenticator(*l.SignedSyslog); err != nil {
			listener.Close()
			return instance{}, err
		}
	}
	if s.selfBounded {
		transport = listener
	}
	if pair, ok := o.certificates[l.ID]; ok {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, NextProtos: []string{adapter.FramedALPN}}
		switch s.alpn {
		case noALPN:
			tlsConfig.NextProtos = nil
		case coapALPN:
			tlsConfig.NextProtos = []string{adapter.CoAPALPN}
		}
		if server != nil {
			tlsConfig = server.TLSConfig.Clone()
			tlsConfig.Certificates = []tls.Certificate{pair}
		}
		if syslog != nil {
			if tlsConfig, err = syslog.TLSConfig(tlsConfig); err != nil {
				_ = transport.Close()
				return instance{}, err
			}
		}
		transport = tls.NewListener(transport, tlsConfig)
	}
	serve := func() error {
		return s.serve(o.work, served{l: l, d: d, listener: transport, slots: o.slots, requests: o.requests, sessionsPerAuthor: o.c.Limits.SessionsPerAuthor, realm: o.c.Realm, syslog: syslog, signedSyslog: signedSyslog, sctp: s.sctp})
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

// bridge is one prepared top-level bridge.
type bridge interface {
	bridgeID() string
	serve(*startup) error
	// release clears its credentials or closes its server, once it joined
	// or when a later preparation fails.
	release()
}

type prepareBridge func(config.Config, *store.Store, *backend.Backend, *auth.CursorCodec) ([]bridge, error)

// bridgeOwners prepare each Bridge transport in config, keyed by name;
// preparation never dials.
var bridgeOwners = map[string]prepareBridge{
	"redis/pubsub":           bridges(prepareRedis),
	"redis/streams":          bridges(prepareRedisStreams),
	"irc":                    bridges(prepareIRC),
	"xmpp":                   bridges(prepareXMPP),
	"sftp":                   bridges(prepareSFTP),
	"postgres/listen-notify": bridges(preparePostgresNotify),
}

func bridges[O bridge](prepare func(config.Config, *store.Store, *backend.Backend, *auth.CursorCodec) ([]O, error)) prepareBridge {
	return func(c config.Config, s *store.Store, b *backend.Backend, cursors *auth.CursorCodec) ([]bridge, error) {
		owners, err := prepare(c, s, b, cursors)
		if err != nil {
			return nil, err
		}
		prepared := make([]bridge, len(owners))
		for i, o := range owners {
			prepared[i] = o
		}
		return prepared, nil
	}
}
