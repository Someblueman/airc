package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const (
	udpSourceIdle   = 5 * time.Second
	udpErrorReserve = 4 << 10
	udpWireBudget   = 1 << 20
)

// UDPReturnRoute fixes the authenticated principal and binding to one
// encrypted response destination. The source address is never used as
// authority or as a response destination.
type UDPReturnRoute struct {
	ID          string
	Principal   string
	Binding     string
	RecipientID string
	Destination netip.AddrPort
	Recipient   *ecdsa.PublicKey
}

type udpSource struct {
	last    time.Time
	partial *datagramAssembler
	active  map[string]struct{}
}

// UDPDatagrams owns one bounded UDP ingress socket. It deliberately does not
// implement a PacketConn: each authenticated request is routed to its fixed
// owner-provisioned return destination.
type UDPDatagrams struct {
	d         *dispatch.Dispatcher
	sealer    *auth.ResponseSealer
	realm     string
	routes    map[string]UDPReturnRoute
	slots     chan struct{}
	requests  chan struct{}
	mu        sync.Mutex
	sources   map[netip.AddrPort]*udpSource
	active    map[string]struct{}
	writeSlot chan struct{}
}

func NewUDPDatagrams(d *dispatch.Dispatcher, sealer *auth.ResponseSealer, realm string, routes []UDPReturnRoute, slots, requests chan struct{}) (*UDPDatagrams, error) {
	if d == nil || sealer == nil || !validUDPID(realm) || cap(slots) < 1 || cap(requests) < 1 {
		return nil, store.ErrInvalid
	}
	u := &UDPDatagrams{d: d, sealer: sealer, realm: realm, routes: make(map[string]UDPReturnRoute), slots: slots, requests: requests, sources: make(map[netip.AddrPort]*udpSource), active: make(map[string]struct{}), writeSlot: make(chan struct{}, 1)}
	principals := make(map[string]struct{})
	for _, route := range routes {
		if !validUDPRoute(route) || u.routes[route.Principal+"\x00"+route.Binding] != (UDPReturnRoute{}) {
			return nil, store.ErrInvalid
		}
		if _, exists := principals[route.Principal]; exists {
			return nil, store.ErrInvalid
		}
		principals[route.Principal] = struct{}{}
		route.Recipient = cloneUDPPublicKey(route.Recipient)
		if route.Recipient == nil {
			return nil, store.ErrInvalid
		}
		u.routes[route.Principal+"\x00"+route.Binding] = route
	}
	if len(u.routes) == 0 {
		return nil, store.ErrInvalid
	}
	return u, nil
}

func (u *UDPDatagrams) Serve(parent context.Context, conn *net.UDPConn) error {
	if u == nil || conn == nil {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	var workers sync.WaitGroup
	defer func() {
		cancel()
		_ = conn.Close()
		if !stop() {
			<-closed
		}
		workers.Wait()
		u.releaseSources()
	}()
	buf := make([]byte, agd1DatagramBytes+1)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				u.reapSources(time.Now())
				continue
			}
			return err
		}
		u.reapSources(time.Now())
		if n == 0 || n > agd1DatagramBytes {
			continue
		}
		// Reject malformed AGD1 before allocating a source or consuming the
		// shared admission slot.
		if _, _, _, _, _, err := parseAGD1Datagram(buf[:n]); err != nil {
			continue
		}
		sourceAddr, err := netip.ParseAddrPort(addr.String())
		if err != nil {
			continue
		}
		source, ok := u.source(sourceAddr)
		if !ok {
			continue
		}
		body, err := source.partial.add(buf[:n], time.Now())
		if err != nil || len(body) == 0 {
			continue
		}
		var request FrameRequest
		if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Profile != "encrypted" || request.Envelope == "" {
			continue
		}
		verified, err := u.d.AuthenticateDetached(request.Envelope, true, time.Now())
		if err != nil {
			continue
		}
		route, ok := u.routes[verified.PrincipalID()+"\x00"+verified.Binding()]
		if !ok {
			continue
		}
		u.mu.Lock()
		source.last = time.Now()
		u.mu.Unlock()
		key := verified.PrincipalID() + "\x00" + route.ID + "\x00" + request.CorrelationID
		u.mu.Lock()
		if _, duplicate := u.active[key]; duplicate {
			u.mu.Unlock()
			continue
		}
		select {
		case u.requests <- struct{}{}:
			u.active[key] = struct{}{}
			source.active[key] = struct{}{}
		default:
			u.mu.Unlock()
			continue
		}
		u.mu.Unlock()
		workers.Go(func() {
			defer func() {
				<-u.requests
				u.mu.Lock()
				delete(u.active, key)
				delete(source.active, key)
				u.mu.Unlock()
			}()
			requestCtx, finish := context.WithTimeout(ctx, 4*time.Second)
			if request.Stream {
				finish()
				requestCtx, finish = context.WithTimeout(ctx, 20*time.Second)
			}
			defer finish()
			write := u.writer(requestCtx, conn, route, verified)
			runCorrelatedVerifiedRequest(requestCtx, write, []string{"long_poll", "udp_mailbox_datagrams"}, true, u.d, request, verified)
		})
	}
}

func (u *UDPDatagrams) source(addr netip.AddrPort) (*udpSource, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	source := u.sources[addr]
	if source == nil {
		select {
		case u.slots <- struct{}{}:
		default:
			return nil, false
		}
		source = &udpSource{last: time.Now(), partial: newDatagramAssembler(), active: make(map[string]struct{})}
		u.sources[addr] = source
	}
	return source, true
}

func (u *UDPDatagrams) reapSources(now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for addr, source := range u.sources {
		if len(source.active) == 0 && now.Sub(source.last) >= udpSourceIdle {
			delete(u.sources, addr)
			<-u.slots
		}
	}
}

func (u *UDPDatagrams) releaseSources() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for addr := range u.sources {
		delete(u.sources, addr)
		<-u.slots
	}
}

func (u *UDPDatagrams) writer(ctx context.Context, conn *net.UDPConn, route UDPReturnRoute, verified dispatch.VerifiedMessage) func(FrameResponse) error {
	var wireBytes int
	return func(response FrameResponse) error {
		if response.Result != nil && response.Result.Capabilities != nil {
			response.Result.Capabilities.MaxResponseFrameBytes = auth.MaxResponsePayloadBytes
		}
		payload, err := json.Marshal(response)
		if err != nil || len(payload) > auth.MaxResponsePayloadBytes {
			return store.ErrCapacity
		}
		token, err := u.sealer.Seal(auth.ResponseScope{Realm: u.realm, Principal: verified.PrincipalID(), Binding: verified.Binding(), Route: route.ID}, route.RecipientID, route.Recipient, payload, time.Now())
		if errors.Is(err, auth.ErrResponseCapacity) {
			return store.ErrCapacity
		}
		if err != nil {
			return err
		}
		fragments, err := fragmentAGD1([]byte(token))
		if err != nil {
			return err
		}
		wire := 0
		for _, fragment := range fragments {
			wire += len(fragment)
		}
		limit := udpWireBudget - udpErrorReserve
		if response.Event == "error" || response.Event == "end" {
			limit = udpWireBudget
		}
		if wireBytes+wire > limit {
			return store.ErrCapacity
		}
		writeCtx, finish := context.WithTimeout(ctx, 3*time.Second)
		defer finish()
		select {
		case u.writeSlot <- struct{}{}:
		case <-writeCtx.Done():
			return writeCtx.Err()
		}
		defer func() { <-u.writeSlot }()
		if writeCtx.Err() != nil {
			return writeCtx.Err()
		}
		deadline, _ := writeCtx.Deadline()
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer conn.SetWriteDeadline(time.Time{})
		destination := &net.UDPAddr{IP: route.Destination.Addr().AsSlice(), Port: int(route.Destination.Port()), Zone: route.Destination.Addr().Zone()}
		for _, fragment := range fragments {
			if writeCtx.Err() != nil {
				return writeCtx.Err()
			}
			if _, err := conn.WriteToUDP(fragment, destination); err != nil {
				return err
			}
		}
		wireBytes += wire
		return nil
	}
}

func validUDPID(id string) bool {
	return len(id) > 0 && len(id) <= 64 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == ""
}

func validUDPRoute(route UDPReturnRoute) bool {
	for _, id := range []string{route.ID, route.Principal, route.Binding, route.RecipientID} {
		if !validUDPID(id) {
			return false
		}
	}
	addr := route.Destination.Addr().Unmap()
	if !route.Destination.IsValid() || route.Destination.Port() == 0 || addr.IsUnspecified() || addr.IsMulticast() || addr == netip.MustParseAddr("255.255.255.255") || route.Recipient == nil || route.Recipient.Curve != elliptic.P256() {
		return false
	}
	_, err := route.Recipient.ECDH()
	return err == nil
}

func cloneUDPPublicKey(key *ecdsa.PublicKey) *ecdsa.PublicKey {
	bytes, err := key.Bytes()
	if err != nil {
		return nil
	}
	copy, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), bytes)
	if err != nil {
		return nil
	}
	return copy
}
