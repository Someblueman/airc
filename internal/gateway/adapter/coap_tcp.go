package adapter

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapNet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/net/monitor/inactivity"
	"github.com/plgd-dev/go-coap/v3/net/responsewriter"
	coaptcp "github.com/plgd-dev/go-coap/v3/tcp/client"
	"github.com/plgd-dev/go-coap/v3/tcp/coder"
)

const coapTCPReceiveFrame = 1152

// ServeCoAPTCP owns one already TLS-handshaken carrier and joins its read loop,
// expiry loop, admitted handlers and watches. The private dependency queue
// stops through Session.Done; the closed handler gate prevents late native work.
// The service enforces TLS 1.3 and CoAP ALPN.
func ServeCoAPTCP(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}) error {
	if conn == nil || d == nil || cap(requests) < 1 || cap(requests) > 256 {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	state := newCoAPState(requests, "coaps_tcp_observe_agd1")
	carrier := &coapTCPConn{Conn: conn, state: state, peerMax: coapTCPReceiveFrame}
	carrier.authenticated.Store(time.Now().UnixNano())
	ledger := newCoAPLedger(nil)
	cfg := coaptcp.DefaultConfig
	cfg.Ctx = ctx
	cfg.MaxMessageSize = coapTCPReceiveFrame
	cfg.BlockwiseEnable = true
	cfg.BlockwiseSZX = blockwise.SZX1024
	cfg.BlockwiseTransferTimeout = 3 * time.Second
	cfg.ReceivedMessageQueueSize = 16
	cfg.ConnectionCacheSize = 16
	cfg.DisableTCPSignalMessageCSM = true
	cfg.DisablePeerTCPSignalMessageCSMs = false
	cfg.CloseSocket = true
	cfg.Errors = func(error) {}
	cfg.Handler = func(w *responsewriter.ResponseWriter[*coaptcp.Conn], req *pool.Message) {
		if !state.beginHandler() {
			_ = w.Conn().Close()
			return
		}
		defer state.endHandler()
		peer := &coapTCPPeer{cc: w.Conn(), carrier: carrier}
		serveCoAPRequest(ctx, w, peer, req, d, state)
		if w.Message().IsModified() {
			_ = peer.WriteMessage(w.Message())
		}
		// Suppress the library's automatic response: it bypasses the bounded
		// application write and cannot establish transport-visible readiness.
		w.SetMessage(w.Conn().AcquireMessage(ctx))
	}
	cc := coaptcp.NewConnWithOpts(coapNet.NewConn(carrier), &cfg,
		coaptcp.WithRequestMonitor(func(_ *coaptcp.Conn, req *pool.Message) (bool, error) {
			if req.Code() == codes.CSM {
				return false, carrier.receiveCSM(req)
			}
			if !carrier.hasCSM() {
				return true, store.ErrInvalid
			}
			return ledger.check(req)
		}),
		coaptcp.WithBlockWise(func(c *coaptcp.Conn) *blockwise.BlockWise[*coaptcp.Conn] {
			return blockwise.New(c, 3*time.Second, func(error) {}, nil)
		}),
		coaptcp.WithInactivityMonitor(inactivity.NewNilMonitor[*coaptcp.Conn]()),
	)
	cc.AddOnClose(state.close)
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { state.close(); _ = cc.Close(); close(stopped) })
	periodicDone := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(periodicDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				if (!carrier.hasCSM() && now.Sub(started) >= 5*time.Second) || now.Sub(time.Unix(0, carrier.authenticated.Load())) >= 30*time.Second {
					_ = cc.Close()
					return
				}
				cc.CheckExpirations(now)
				state.expire(now)
			case <-ctx.Done():
				return
			case <-cc.Context().Done():
				return
			}
		}
	}()
	defer func() {
		cancel()
		state.close()
		_ = cc.Close()
		if !stop() {
			<-stopped
		}
		<-periodicDone
		state.workers.Wait()
	}()
	// The pinned library's default CSM omits its capabilities. Send the exact
	// supported receive limit and standard (non-BERT) blockwise capability.
	csm := cc.AcquireMessage(ctx)
	csm.SetCode(codes.CSM)
	csm.SetOptionUint32(message.TCPMaxMessageSize, coapTCPReceiveFrame)
	csm.SetOptionBytes(message.TCPBlockWiseTransfer, nil)
	err := (&coapTCPPeer{cc: cc, carrier: carrier}).WriteMessage(csm)
	cc.ReleaseMessage(csm)
	if err != nil {
		_ = cc.Close()
		// Run the cancelled session to close Session.Done and release the
		// private queue reader even when the initial CSM write failed.
		_ = cc.Run()
		return err
	}
	err = cc.Run()
	if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type coapTCPPeer struct {
	cc      *coaptcp.Conn
	carrier *coapTCPConn
}

func (p *coapTCPPeer) AcquireMessage(ctx context.Context) *pool.Message {
	return p.cc.AcquireMessage(ctx)
}
func (p *coapTCPPeer) ReleaseMessage(m *pool.Message) { p.cc.ReleaseMessage(m) }
func (p *coapTCPPeer) Close() error                   { return p.cc.Close() }
func (p *coapTCPPeer) WriteMessage(m *pool.Message) error {
	ctx, cancel := context.WithTimeout(m.Context(), 3*time.Second)
	defer cancel()
	// Keep the message context intact: blockwise caches reuse it for later
	// continuation requests. The close guard bounds this individual write.
	closed := make(chan struct{})
	// go-coap's physical writer takes an uncancellable mutex. Closing the carrier
	// on cancellation releases an earlier writer and bounds contention as well.
	stop := context.AfterFunc(ctx, func() { _ = p.carrier.Close(); close(closed) })
	err := p.cc.WriteMessage(m)
	if !stop() {
		<-closed
	}
	if err != nil {
		_ = p.cc.Close()
	}
	return err
}

type coapTCPConn struct {
	net.Conn
	state         *coapState
	mu            sync.Mutex
	csm           bool
	peerMax       uint32
	authenticated atomic.Int64
}

func (c *coapTCPConn) hasCSM() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.csm }
func (c *coapTCPConn) receiveCSM(m *pool.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	maxSeen, blockSeen := false, false
	nextMax := c.peerMax
	for _, option := range m.Options() {
		switch option.ID {
		case message.TCPMaxMessageSize:
			if maxSeen || len(option.Value) > 4 {
				return store.ErrInvalid
			}
			maxSeen = true
			value, err := m.GetOptionUint32(option.ID)
			if err != nil {
				return store.ErrInvalid
			}
			nextMax = value
		case message.TCPBlockWiseTransfer:
			if blockSeen || len(option.Value) != 0 {
				return store.ErrInvalid
			}
			blockSeen = true
		default:
			if uint16(option.ID)&1 != 0 {
				return store.ErrInvalid
			}
		}
	}
	c.peerMax = nextMax
	c.csm = true
	return nil
}
func (c *coapTCPConn) Write(wire []byte) (int, error) {
	c.mu.Lock()
	maximum := c.peerMax
	c.mu.Unlock()
	if len(wire) > coapMaxResponse+512 || uint64(len(wire)) > uint64(maximum) {
		_ = c.Close()
		return 0, store.ErrCapacity
	}
	if err := c.Conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		_ = c.Close()
		return 0, err
	}
	total := 0
	for total < len(wire) {
		n, err := c.Conn.Write(wire[total:])
		total += n
		if err != nil {
			_ = c.Close()
			return total, err
		}
		if n == 0 {
			_ = c.Close()
			return total, io.ErrShortWrite
		}
	}
	// Decode only a successfully transmitted whole frame. All writers (including
	// blockwise continuations and signals) pass through this carrier boundary.
	sent := pool.NewMessage(context.Background())
	if n, err := sent.UnmarshalWithDecoder(coder.DefaultCoder, wire); err != nil || n != len(wire) {
		_ = c.Close()
		return total, store.ErrInvalid
	}
	c.state.sent(sent, func() { c.authenticated.Store(time.Now().UnixNano()) })
	return total, nil
}
