package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const (
	WebRTCDataChannelLabel    = "airc-gateway-v1"
	WebRTCDataChannelProtocol = "agd1"
	maxWebRTCOfferBytes       = 64 << 10
	maxWebRTCSDPBytes         = 60 << 10
	webRTCGatherTimeout       = 5 * time.Second
	webRTCPeerTimeout         = 10 * time.Second
	webRTCPeerLifetime        = 2 * time.Minute
)

type webrtcOffer struct {
	Version int    `json:"version"`
	SDP     string `json:"sdp"`
	Mode    string `json:"mode"`
}

type webrtcAnswer struct {
	Version int    `json:"version"`
	SDP     string `json:"sdp"`
}

// WebRTC owns authenticated peer connections. The returned HTTP server only
// handles the HTTPS offer exchange; service code owns its listener and TLS.
type WebRTC struct {
	d            *dispatch.Dispatcher
	v            *auth.Verifier
	requests     chan struct{}
	peerSlots    chan struct{}
	perPrincipal int
	options      config.WebRTC
	origins      map[string]bool

	mu         sync.Mutex
	closed     bool
	peers      map[*webrtcPeer]struct{}
	principals map[string]int
	workers    sync.WaitGroup
}

func WebRTCServer(d *dispatch.Dispatcher, v *auth.Verifier, slots, requests chan struct{}, perPrincipal int, options config.WebRTC, origins []string) (*http.Server, *WebRTC, error) {
	if d == nil || v == nil || cap(slots) < 1 || cap(slots) > 256 || cap(requests) < 2 || perPrincipal < 1 || perPrincipal > cap(slots) {
		return nil, nil, store.ErrInvalid
	}
	if err := config.ValidateOrigins(origins); err != nil {
		return nil, nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, nil, store.ErrInvalid
	}
	if options.ICETransportPolicy == "" {
		options.ICETransportPolicy = "all"
	}
	a := &WebRTC{d: d, v: v, requests: requests, peerSlots: slots, perPrincipal: perPrincipal, options: options, origins: make(map[string]bool, len(origins)), peers: make(map[*webrtcPeer]struct{}), principals: make(map[string]int)}
	for _, origin := range origins {
		a.origins[origin] = true
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(a.handleOffer),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	return server, a, nil
}

func (a *WebRTC) handleOffer(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")
	if values := r.Header.Values("Origin"); len(values) == 1 && a.origins[values[0]] {
		w.Header().Set("Access-Control-Allow-Origin", values[0])
	}
	if r.URL.Path != "/v1/webrtc/offer" {
		httpFailure(w, http.StatusNotFound, core.InvalidRequest)
		return
	}
	if !httpOrigin(w, r, a.origins) {
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || !r.TLS.HandshakeComplete || !a.checkOrigin(r) {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	if len(r.Header.Values("Content-Type")) != 1 {
		httpFailure(w, http.StatusUnsupportedMediaType, core.InvalidRequest)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		httpFailure(w, http.StatusUnsupportedMediaType, core.InvalidRequest)
		return
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) != len("Bearer ")+64 {
		httpFailure(w, http.StatusUnauthorized, core.AuthenticationFailed)
		return
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	identity, err := a.v.Authenticate(token)
	if err != nil {
		httpFailure(w, http.StatusUnauthorized, core.AuthenticationFailed)
		return
	}
	capCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_, err = a.d.Capabilities(capCtx, token)
	cancel()
	if err != nil {
		httpFailure(w, http.StatusForbidden, core.Forbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebRTCOfferBytes))
	if err != nil || len(body) > maxWebRTCOfferBytes {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	var offer webrtcOffer
	if err := decodeWebRTCOffer(body, &offer); err != nil || len(offer.SDP) == 0 || len(offer.SDP) > maxWebRTCSDPBytes || !validWebRTCMode(offer.Mode) || !validWebRTCSDP(offer.SDP) {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	peer, err := a.admitPeer(identity.ID(), identity.Binding())
	if err != nil {
		status := http.StatusTooManyRequests
		if errors.Is(err, store.ErrInvalid) {
			status = http.StatusServiceUnavailable
		}
		httpFailure(w, status, core.CapacityExceeded)
		return
	}
	defer peer.handshakeDone()
	answer, err := peer.answer(r.Context(), offer)
	if err != nil {
		peer.close()
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	peer.start()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func (a *WebRTC) checkOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	return len(values) == 0 || len(values) == 1 && a.origins[values[0]]
}

func (a *WebRTC) admitPeer(principal, binding string) (*webrtcPeer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, store.ErrInvalid
	}
	if a.principals[principal+"\x00"+binding] >= a.perPrincipal {
		return nil, store.ErrCapacity
	}
	select {
	case a.peerSlots <- struct{}{}:
	default:
		return nil, store.ErrCapacity
	}
	peerCtx, peerCancel := context.WithCancel(context.Background())
	peer := &webrtcPeer{adapter: a, principal: principal, binding: binding, ctx: peerCtx, cancel: peerCancel, done: make(chan struct{})}
	peer.mu.Lock()
	peer.work.Add(1)
	peer.mu.Unlock()
	a.principals[principal+"\x00"+binding]++
	a.peers[peer] = struct{}{}
	a.workers.Add(1)
	go func() {
		<-peer.done
		peer.wait()
		a.releasePeer(peer)
		a.workers.Done()
	}()
	return peer, nil
}

func (a *WebRTC) releasePeer(p *webrtcPeer) {
	a.mu.Lock()
	if _, ok := a.peers[p]; ok {
		delete(a.peers, p)
		key := p.principal + "\x00" + p.binding
		a.principals[key]--
		if a.principals[key] == 0 {
			delete(a.principals, key)
		}
		<-a.peerSlots
	}
	a.mu.Unlock()
}

func (a *WebRTC) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		a.workers.Wait()
		return nil
	}
	a.closed = true
	peers := make([]*webrtcPeer, 0, len(a.peers))
	for peer := range a.peers {
		peers = append(peers, peer)
	}
	a.mu.Unlock()
	for _, peer := range peers {
		peer.close()
	}
	a.workers.Wait()
	return nil
}

func decodeWebRTCOffer(body []byte, offer *webrtcOffer) error {
	if err := jsoninput.Decode(body, offer, maxWebRTCOfferBytes); err != nil {
		return err
	}
	if offer.Version != 1 {
		return store.ErrInvalid
	}
	return nil
}

func readWebRTCCredential(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	return auth.ReadICECredential(path)
}
