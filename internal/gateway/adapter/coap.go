package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
	coapserver "github.com/plgd-dev/go-coap/v3/dtls/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapNet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/net/monitor/inactivity"
	"github.com/plgd-dev/go-coap/v3/net/responsewriter"
	coapclient "github.com/plgd-dev/go-coap/v3/udp/client"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

const (
	CoAPALPN        = "coap"
	coapMTU         = 1200
	coapMaxMessage  = MaxResponseFrameBytes
	coapMaxRequest  = MaxRequestFrameBytes
	coapMaxResponse = MaxResponseFrameBytes
	coapMaxMIDs     = 1024
	coapMaxTokens   = 256
	coapMaxBlocks   = 4
)

// CoAPRegistration is a single-use, association-local Observe activation token.
// It carries no AIRC authority independent of its original verified intent.
type CoAPRegistration struct {
	Version          int    `json:"version"`
	CorrelationID    string `json:"correlation_id"`
	ObservationToken string `json:"observation_token"`
	ExpiresInMS      int    `json:"expires_in_ms"`
}

type coapPending struct {
	request  FrameRequest
	verified dispatch.VerifiedMessage
	expires  time.Time
}

type coapState struct {
	mu            sync.Mutex
	closed        bool
	handlers      int
	pending       map[string]coapPending
	active        map[string]*coapWatch
	authenticated map[string]bool
	correlations  map[string]bool
	local         chan struct{}
	requests      chan struct{}
	workers       sync.WaitGroup
	watchMode     string
}

func newCoAPState(requests chan struct{}, watchMode string) *coapState {
	return &coapState{pending: make(map[string]coapPending), active: make(map[string]*coapWatch), authenticated: make(map[string]bool), correlations: make(map[string]bool), local: make(chan struct{}, 4), requests: requests, watchMode: watchMode}
}

func (s *coapState) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	clear(s.pending)
	clear(s.correlations)
	for _, watch := range s.active {
		watch.cancel()
	}
}

func (s *coapState) beginHandler() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.handlers >= 16 {
		return false
	}
	s.handlers++
	s.workers.Add(1)
	return true
}
func (s *coapState) endHandler() {
	s.mu.Lock()
	s.handlers--
	s.mu.Unlock()
	s.workers.Done()
}
func (s *coapState) admit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.local <- struct{}{}:
	default:
		return false
	}
	select {
	case s.requests <- struct{}{}:
		return true
	default:
		<-s.local
		return false
	}
}
func (s *coapState) release() { <-s.requests; <-s.local }
func (s *coapState) expire(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, pending := range s.pending {
		if !now.Before(pending.expires) {
			delete(s.pending, token)
			delete(s.correlations, pending.request.CorrelationID)
		}
	}
}
func (s *coapState) authenticatedToken(token message.Token) {
	s.mu.Lock()
	s.authenticated[string(token)] = true
	s.mu.Unlock()
}
func (s *coapState) reset(mid int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, watch := range s.active {
		if watch.mid == mid {
			watch.cancel()
		}
	}
}

// coapSession replaces the dependency's mutex-only write path with cancellable
// admission and one deadline for both waiting and the actual DTLS record write.
// Its inherited reader owns library parsing and bounded Block1/Block2 handling.
type coapSession struct {
	*coapserver.Session
	conn     net.Conn
	writes   chan struct{}
	state    *coapState
	activity func()
}

func (s *coapSession) WriteMessage(msg *pool.Message) error {
	ctx, cancel := context.WithTimeout(msg.Context(), 3*time.Second)
	defer cancel()
	select {
	case s.writes <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.writes }()
	if err := ctx.Err(); err != nil {
		return err
	}
	wire, err := msg.MarshalWithEncoder(coder.DefaultCoder)
	if err != nil {
		return err
	}
	if len(wire) > coapMTU {
		return store.ErrCapacity
	}
	deadline, _ := ctx.Deadline()
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	s.state.setNotificationMID(msg)
	n, err := s.conn.Write(wire)
	if err != nil {
		return err
	}
	if n != len(wire) {
		return io.ErrShortWrite
	}
	s.state.sent(msg, s.activity)
	return nil
}

// ServeCoAP owns one already-handshaken DTLS association. It closes the carrier
// and joins its expiry, handler and native watch workers on every return path.
// Dependency reader cleanup is driven by Session.Done; late queued handlers are
// denied by the closed gate and cannot create native work after the join.
func ServeCoAP(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, activity func()) error {
	if conn == nil || d == nil || cap(requests) < 1 || cap(requests) > 256 || activity == nil {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	state := newCoAPState(requests, "coaps_observe_agd1")
	session := &coapSession{Session: coapserver.NewSession(ctx, coapNet.NewConn(conn), coapMaxMessage, coapMTU, true), conn: conn, writes: make(chan struct{}, 1), state: state, activity: activity}
	cfg := coapclient.DefaultConfig
	cfg.Ctx = ctx
	cfg.MaxMessageSize = coapMaxMessage
	cfg.BlockwiseEnable = true
	cfg.BlockwiseSZX = blockwise.SZX1024
	cfg.BlockwiseTransferTimeout = 3 * time.Second
	cfg.ReceivedMessageQueueSize = 16
	cfg.MTU = coapMTU
	cfg.TransmissionMaxRetransmit = 2
	cfg.TransmissionAcknowledgeTimeout = time.Second
	cfg.Errors = func(error) {}
	cfg.Handler = func(w *responsewriter.ResponseWriter[*coapclient.Conn], req *pool.Message) {
		if !state.beginHandler() {
			return
		}
		defer state.endHandler()
		serveCoAPRequest(ctx, w, w.Conn(), req, d, state)
	}
	ledger := newCoAPLedger(state.reset)
	cc := coapclient.NewConnWithOpts(session, &cfg,
		coapclient.WithRequestMonitor(func(_ *coapclient.Conn, req *pool.Message) (bool, error) {
			return ledger.check(req)
		}),
		coapclient.WithResponseMessageCache(newRefusingCache(coapMaxMIDs)),
		coapclient.WithBlockWise(func(c *coapclient.Conn) *blockwise.BlockWise[*coapclient.Conn] {
			return blockwise.New(c, 3*time.Second, func(error) {}, nil)
		}),
		coapclient.WithInactivityMonitor(inactivity.NewNilMonitor[*coapclient.Conn]()),
	)
	cc.AddOnClose(state.close)
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { state.close(); _ = session.Close(); close(stopped) })
	periodicDone := make(chan struct{})
	go func() {
		defer close(periodicDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				cc.CheckExpirations(now)
				state.expire(now)
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() {
		cancel()
		state.close()
		_ = session.Close()
		if !stop() {
			<-stopped
		}
		<-periodicDone
		state.workers.Wait()
	}()
	err := cc.Run()
	if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func coapJSON(w coapResponder, code codes.Code, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return store.ErrInvalid
	}
	if len(body) > coapMaxResponse {
		return store.ErrCapacity
	}
	if err := w.SetResponse(code, message.AppJSON, bytes.NewReader(body)); err != nil {
		return err
	}
	w.Message().SetOptionUint32(message.MaxAge, 0)
	return nil
}

func serveCoAPRequest(ctx context.Context, w coapResponder, peer coapPeer, req *pool.Message, d *dispatch.Dispatcher, state *coapState) {
	path, err := req.Path()
	if err != nil {
		_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
		return
	}
	if path == "/v1/coap/observe" {
		serveCoAPObserve(ctx, w, peer, req, d, state)
		return
	}
	if path != "/v1/coap/rpc" && path != "/v1/coap/observe/register" {
		_ = w.SetResponse(codes.NotFound, message.TextPlain, nil)
		return
	}
	if req.Code() != codes.POST {
		_ = w.SetResponse(codes.MethodNotAllowed, message.TextPlain, nil)
		return
	}
	format, err := req.ContentFormat()
	if err != nil || format != message.AppJSON {
		_ = w.SetResponse(codes.UnsupportedMediaType, message.TextPlain, nil)
		return
	}
	if !state.admit() {
		_ = w.SetResponse(codes.ServiceUnavailable, message.TextPlain, nil)
		return
	}
	defer state.release()
	body, err := req.ReadBody()
	var fr FrameRequest
	if err != nil || jsoninput.Decode(body, &fr, coapMaxRequest) != nil || fr.Version != 1 || !correlationID(fr.CorrelationID) || (fr.Profile != "signed" && fr.Profile != "encrypted") || fr.Envelope == "" {
		_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
		return
	}
	if fr.Stream != (path == "/v1/coap/observe/register") {
		_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
		return
	}
	state.mu.Lock()
	if state.closed || state.correlations[fr.CorrelationID] {
		state.mu.Unlock()
		state.close()
		_ = peer.Close()
		return
	}
	state.correlations[fr.CorrelationID] = true
	state.mu.Unlock()
	retained := false
	defer func() {
		if !retained {
			state.mu.Lock()
			delete(state.correlations, fr.CorrelationID)
			state.mu.Unlock()
		}
	}()
	verified, err := d.AuthenticateDetached(fr.Envelope, fr.Profile == "encrypted", time.Now())
	if err != nil {
		_ = w.SetResponse(codes.Unauthorized, message.TextPlain, nil)
		return
	}
	if fr.Stream {
		if !verified.Operation().IsWatch() {
			_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
			return
		}
		var token [8]byte
		if _, err := rand.Read(token[:]); err != nil {
			_ = w.SetResponse(codes.InternalServerError, message.TextPlain, nil)
			return
		}
		key := string(token[:])
		state.expire(time.Now())
		state.mu.Lock()
		if state.closed || len(state.pending)+len(state.active) >= 4 {
			state.mu.Unlock()
			_ = w.SetResponse(codes.ServiceUnavailable, message.TextPlain, nil)
			return
		}
		_, pendingCollision := state.pending[key]
		_, activeCollision := state.active[key]
		if pendingCollision || activeCollision {
			state.mu.Unlock()
			_ = w.SetResponse(codes.InternalServerError, message.TextPlain, nil)
			return
		}
		state.pending[key] = coapPending{request: fr, verified: verified, expires: time.Now().Add(10 * time.Second)}
		retained = true
		state.mu.Unlock()
		state.authenticatedToken(req.Token())
		_ = coapJSON(w, codes.Content, CoAPRegistration{Version: 1, CorrelationID: fr.CorrelationID, ObservationToken: hex.EncodeToString(token[:]), ExpiresInMS: 10000})
		return
	}
	var response FrameResponse
	runCorrelatedVerifiedRequest(ctx, func(r FrameResponse) error {
		encoded, err := json.Marshal(r)
		if err != nil || len(encoded) > coapMaxResponse {
			return store.ErrCapacity
		}
		response = r
		return nil
	}, []string{"long_poll", state.watchMode}, true, d, fr, verified)
	code := codes.Content
	if response.Error != nil {
		code = codes.BadRequest
	} else {
		state.authenticatedToken(req.Token())
	}
	if coapJSON(w, code, response) != nil {
		_ = w.SetResponse(codes.InternalServerError, message.TextPlain, nil)
	}
}
