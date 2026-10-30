package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
)

const (
	coapMTU         = 1200
	coapMaxMessage  = MaxResponseFrameBytes
	coapMaxRequest  = MaxRequestFrameBytes
	coapMaxResponse = MaxResponseFrameBytes
	coapMaxMIDs     = 1024
	coapMaxTokens   = 256
	coapMaxBlocks   = 4
)

// CoAPRegistration is a single-use, peer-local Observe activation token.
// It carries no AIRC authority beyond its original request.
type CoAPRegistration struct {
	Version          int    `json:"version"`
	CorrelationID    string `json:"correlation_id"`
	ObservationToken string `json:"observation_token"`
	ExpiresInMS      int    `json:"expires_in_ms"`
}

type coapPending struct {
	request FrameRequest
	plain   []byte // the frame's contract request, run as its author
	expires time.Time
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
	// plain is the client source and, on an association or connection, its
	// session: each frame runs as its author. Nil refuses every frame.
	plain *dispatch.Ingress
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
	var plain []byte
	if err == nil && len(body) <= coapMaxRequest {
		fr, plain, err = decodeFrame(body)
	}
	if err != nil || fr.Version != 1 || !correlationID(fr.CorrelationID) || fr.Profile != "" && fr.Profile != "plain" || plain == nil || state.plain == nil {
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
	// The request names its author; AIRC checks it when it runs.
	sub, err := core.DecodeSubmission(plain)
	if err != nil {
		_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
		return
	}
	operation := sub.Request.Operation
	if fr.Stream {
		if !operation.IsWatch() {
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
		state.pending[key] = coapPending{request: fr, plain: plain, expires: time.Now().Add(10 * time.Second)}
		retained = true
		state.mu.Unlock()
		state.authenticatedToken(req.Token())
		_ = coapJSON(w, codes.Content, CoAPRegistration{Version: 1, CorrelationID: fr.CorrelationID, ObservationToken: hex.EncodeToString(token[:]), ExpiresInMS: 10000})
		return
	}
	var response FrameResponse
	modes := []string{"long_poll"}
	if state.watchMode != "" {
		modes = append(modes, state.watchMode)
	}
	write := func(r FrameResponse) error {
		encoded, err := json.Marshal(r)
		if err != nil || len(encoded) > coapMaxResponse {
			return store.ErrCapacity
		}
		response = r
		return nil
	}
	runCorrelatedPlainRequest(ctx, write, modes, true, d, fr, *state.plain, plain)
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
