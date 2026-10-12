package adapter

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapNet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/net/monitor/inactivity"
	"github.com/plgd-dev/go-coap/v3/net/responsewriter"
	coapclient "github.com/plgd-dev/go-coap/v3/udp/client"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

// coapUDPIdle closes a CoAP UDP peer's state after this long without traffic.
const coapUDPIdle = 30 * time.Second

// ServeCoAPUDP serves plain author requests as ordinary CoAP over UDP
// (RFC 7252), confirmable or not: POST /v1/coap/rpc with a plain frame, the
// Observe registration on /v1/coap/observe/register and /v1/coap/observe,
// answered in the response (piggybacked or separate) with Block2 for large
// results. Each request names its author and token: an unverified UDP source
// has no session, so there is no identify. A reply is at most one block per
// request packet, which bounds reflection to a spoofed source.
//
// It demultiplexes the socket by source itself rather than through go-coap's
// UDP server, which owns its per-source sessions and so offers no write hook.
// Each source is a go-coap connection over a coapUDPSession, so the adapter
// sees every datagram it writes, as it does on a DTLS association.
func ServeCoAPUDP(parent context.Context, conn *net.UDPConn, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	if conn == nil || !d.AuthorAccess() || cap(slots) < 1 || cap(requests) < 1 || cap(requests) > 256 {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	s := &coapUDPServer{ctx: ctx, l: coapNet.NewUDPConn("udp", conn), d: d, slots: slots, requests: requests, peers: make(map[coapUDPKey]*coapUDPPeer)}
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = s.l.Close(); close(stopped) })
	expired := make(chan struct{})
	go s.checkExpirations(expired)
	err := s.serve()
	cancel()
	if !stop() {
		<-stopped
	}
	<-expired
	s.closePeers()
	s.work.Wait()
	if parent.Err() != nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type coapUDPServer struct {
	ctx             context.Context
	l               *coapNet.UDPConn
	d               *dispatch.Dispatcher
	slots, requests chan struct{}
	mu              sync.Mutex
	peers           map[coapUDPKey]*coapUDPPeer
	work            sync.WaitGroup
}

// coapUDPKey names a peer by its source and the local address its datagrams
// arrive at, which can differ on a wildcard socket, as go-coap's UDP server
// does.
type coapUDPKey struct {
	remote netip.AddrPort
	local  netip.Addr
}

type coapUDPPeer struct {
	cc     *coapclient.Conn
	state  *coapState
	ledger *coapLedger
}

func (s *coapUDPServer) serve() error {
	buf := make([]byte, coapMaxMessage)
	for {
		var raddr *net.UDPAddr
		var cm *coapNet.ControlMessage
		n, err := s.l.ReadWithOptions(buf, coapNet.WithContext(s.ctx), coapNet.WithGetControlMessage(&cm), coapNet.WithGetRemoteAddr(&raddr))
		if err != nil {
			if s.ctx.Err() != nil || coapNet.IsCancelOrCloseError(err) {
				return nil
			}
			return err
		}
		peer := s.peer(raddr, cm)
		if peer == nil {
			continue
		}
		if err := peer.cc.Process(cm, buf[:n]); err != nil {
			_ = peer.cc.Close()
		}
	}
}

// peer is raddr's live peer, a new one if a slot is free, or nil.
func (s *coapUDPServer) peer(raddr *net.UDPAddr, cm *coapNet.ControlMessage) *coapUDPPeer {
	if raddr == nil {
		return nil
	}
	key := coapUDPKey{remote: raddr.AddrPort()}
	if cm != nil && !cm.Dst.IsMulticast() {
		key.local, _ = netip.AddrFromSlice(cm.Dst)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if peer := s.peers[key]; peer != nil && peer.cc.Context().Err() == nil {
		return peer
	}
	// A closed peer keeps its slot until its workers join.
	select {
	case s.slots <- struct{}{}:
	default:
		return nil
	}
	peer := s.newPeer(key, raddr)
	s.peers[key] = peer
	return peer
}

func (s *coapUDPServer) newPeer(key coapUDPKey, raddr *net.UDPAddr) *coapUDPPeer {
	state := newCoAPState(s.requests, "coap_udp_observe_fragments")
	// No session: a UDP source is unverified, so every request names its own
	// author.
	state.plain = &dispatch.Ingress{Source: s.d.Source(raddr)}
	peer := &coapUDPPeer{state: state, ledger: newCoAPLedger(state.reset)}
	cfg := coapclient.DefaultConfig
	cfg.TransmissionNStart = 1
	cfg.TransmissionAcknowledgeTimeout = time.Second
	cfg.TransmissionMaxRetransmit = 2
	cfg.BlockwiseSZX = blockwise.SZX1024
	cfg.ReceivedMessageQueueSize = 16
	cfg.Errors = func(error) {}
	cfg.Handler = func(w *responsewriter.ResponseWriter[*coapclient.Conn], req *pool.Message) {
		if !state.beginHandler() {
			_ = w.SetResponse(codes.ServiceUnavailable, 0, nil)
			return
		}
		defer state.endHandler()
		serveCoAPRequest(s.ctx, w, w.Conn(), req, s.d, state)
	}
	peer.cc = coapclient.NewConnWithOpts(newCoAPUDPSession(s.ctx, s.l, raddr, state), &cfg,
		coapclient.WithRequestMonitor(func(_ *coapclient.Conn, req *pool.Message) (bool, error) {
			return peer.ledger.check(req)
		}),
		coapclient.WithBlockWise(func(cc *coapclient.Conn) *blockwise.BlockWise[*coapclient.Conn] {
			return blockwise.New(cc, 3*time.Second, func(error) {}, nil)
		}),
		coapclient.WithInactivityMonitor(inactivity.New(coapUDPIdle, func(cc *coapclient.Conn) { _ = cc.Close() })),
	)
	s.work.Add(1)
	peer.cc.AddOnClose(func() {
		state.close()
		go func() {
			state.workers.Wait()
			s.mu.Lock()
			if s.peers[key] == peer {
				delete(s.peers, key)
			}
			s.mu.Unlock()
			<-s.slots
			s.work.Done()
		}()
	})
	return peer
}

// checkExpirations drives each peer's retransmissions and its block, cache
// and inactivity expiry, as ServeCoAP does for its association.
func (s *coapUDPServer) checkExpirations(done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var peers []*coapUDPPeer
	for {
		select {
		case now := <-ticker.C:
			peers = s.listPeers(peers[:0])
			for _, peer := range peers {
				peer.cc.CheckExpirations(now)
			}
			clear(peers)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *coapUDPServer) listPeers(peers []*coapUDPPeer) []*coapUDPPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	return peers
}

func (s *coapUDPServer) closePeers() {
	for _, peer := range s.listPeers(nil) {
		_ = peer.cc.Close()
	}
}

// coapUDPSession is one source's association over the shared socket, in place
// of go-coap's udp/server Session. Its WriteMessage is this carrier's write
// hook, as coapSession.WriteMessage is for DTLS: it records a confirmable
// notification's message ID before the datagram leaves, so a Reset naming it
// cancels the watch (coapLedger.check, coapState.reset), and reports each
// message once written, which opens a watch's ready gate after its Observe 0
// response.
type coapUDPSession struct {
	ctx     atomic.Pointer[context.Context]
	cancel  context.CancelFunc
	conn    *coapNet.UDPConn
	raddr   *net.UDPAddr
	state   *coapState
	mu      sync.Mutex
	closed  bool
	onClose []func()
	done    chan struct{}
}

func newCoAPUDPSession(parent context.Context, conn *coapNet.UDPConn, raddr *net.UDPAddr, state *coapState) *coapUDPSession {
	ctx, cancel := context.WithCancel(parent)
	s := &coapUDPSession{cancel: cancel, conn: conn, raddr: raddr, state: state, done: make(chan struct{})}
	s.ctx.Store(&ctx)
	return s
}

func (s *coapUDPSession) WriteMessage(msg *pool.Message) error {
	wire, err := msg.MarshalWithEncoder(coder.DefaultCoder)
	if err != nil {
		return err
	}
	s.state.setNotificationMID(msg)
	if err := s.conn.WriteWithOptions(wire, coapNet.WithContext(msg.Context()), coapNet.WithRemoteAddr(s.raddr), coapNet.WithControlMessage(msg.ControlMessage())); err != nil {
		return err
	}
	s.state.sent(msg, nil)
	return nil
}

// Close ends the association and runs its close callbacks once. The shared
// socket stays open.
func (s *coapUDPSession) Close() error {
	s.cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	onClose := s.onClose
	s.onClose = nil
	s.mu.Unlock()
	for _, f := range onClose {
		f()
	}
	close(s.done)
	return nil
}

func (s *coapUDPSession) AddOnClose(f func()) {
	s.mu.Lock()
	if !s.closed {
		s.onClose = append(s.onClose, f)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	f()
}

func (s *coapUDPSession) Context() context.Context { return *s.ctx.Load() }
func (s *coapUDPSession) SetContextValue(key, val any) {
	ctx := context.WithValue(s.Context(), key, val)
	s.ctx.Store(&ctx)
}
func (s *coapUDPSession) Done() <-chan struct{}  { return s.done }
func (s *coapUDPSession) MaxMessageSize() uint32 { return coapMaxMessage }
func (s *coapUDPSession) RemoteAddr() net.Addr   { return s.raddr }
func (s *coapUDPSession) LocalAddr() net.Addr    { return s.conn.LocalAddr() }
func (s *coapUDPSession) NetConn() net.Conn      { return s.conn.NetConn() }

// Run is unused: coapUDPServer.serve reads the shared socket for every peer.
func (s *coapUDPSession) Run(*coapclient.Conn) error { return errors.ErrUnsupported }

// WriteMulticastMessage is unused: the server never writes multicast.
func (s *coapUDPSession) WriteMulticastMessage(*pool.Message, *net.UDPAddr, ...coapNet.MulticastOption) error {
	return errors.ErrUnsupported
}
