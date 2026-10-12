package adapter

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// amplificationFactor bounds what an unverified datagram source can be sent
// for one request, as in QUIC's address validation (RFC 9000, section 8):
// at most three times the request's own wire bytes. A source address is
// spoofable, so a larger reply would make the listener a reflector. Loopback
// sources cannot be spoofed from the network and are exempt.
const amplificationFactor = 3

// amplificationReserve lets a final error or end frame through after the
// budget is spent, so a client learns why its reply stopped.
const amplificationReserve = 512

// AuthorPackets serves plain author requests on a packet carrier. Each
// request is a plain correlated frame (a contract request naming its author)
// split into datagram fragments; its results are plain sequenced frames,
// fragmented the same way. There is no connection, so there is no identify.
//
// On a network carrier (NewAuthorDatagrams) a request names its author, with
// its account token when registered; the token travels in clear. Replies go
// back to the request's source within an amplification budget.
//
// On a local carrier (NewLocalAuthorPackets) the carrier's owner-only
// boundary is the access gate and AIRC sees listener:<id> as the source.
type AuthorPackets struct {
	d *dispatch.Dispatcher
	// local selects the local-carrier behaviour; bound and reply apply only
	// to it.
	local     bool
	bound     *dispatch.BoundAuthor
	reply     net.Addr
	slots     chan struct{}
	requests  chan struct{}
	mu        sync.Mutex
	sources   map[string]*packetSource
	active    map[string]struct{}
	writeSlot chan struct{}
}

// NewAuthorDatagrams serves a network packet socket: UDP, UDP-Lite, IPv4/IPv6
// multicast, IPv4 broadcast or ICMPv6 Echo.
func NewAuthorDatagrams(d *dispatch.Dispatcher, slots, requests chan struct{}) (*AuthorPackets, error) {
	return newAuthorPackets(d, false, nil, nil, slots, requests)
}

// NewLocalAuthorPackets serves a local packet carrier: Unix datagrams, POSIX
// and System V message queues, shared memory and mapped files. Replies go to
// reply when it is set (queues, shared memory, mapped files), else to the
// sender's own bound address (Unix datagrams). A sender without an address
// gets no reply; its requests still run, and receipt.get on any carrier
// reports them.
func NewLocalAuthorPackets(d *dispatch.Dispatcher, bound *dispatch.BoundAuthor, reply net.Addr, slots, requests chan struct{}) (*AuthorPackets, error) {
	if reply != nil && !validPacketDestination(reply) {
		return nil, store.ErrInvalid
	}
	return newAuthorPackets(d, true, bound, clonePacketAddr(reply), slots, requests)
}

func newAuthorPackets(d *dispatch.Dispatcher, local bool, bound *dispatch.BoundAuthor, reply net.Addr, slots, requests chan struct{}) (*AuthorPackets, error) {
	if d == nil || !d.AuthorAccess() || cap(slots) < 1 || cap(requests) < 1 {
		return nil, store.ErrInvalid
	}
	return &AuthorPackets{d: d, local: local, bound: bound, reply: reply, slots: slots, requests: requests, sources: make(map[string]*packetSource), active: make(map[string]struct{}), writeSlot: make(chan struct{}, 1)}, nil
}

// Serve reads until ctx ends, then closes conn and joins admitted work.
func (u *AuthorPackets) Serve(parent context.Context, conn net.PacketConn) error {
	if u == nil || conn == nil || conn.LocalAddr() == nil {
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
	modes := []string{"long_poll", packetWatchMode(conn)}
	buf := make([]byte, fragmentDatagramBytes+1)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		n, addr, err := conn.ReadFrom(buf)
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
		if n == 0 || n > fragmentDatagramBytes {
			continue
		}
		// Reject malformed fragments before allocating a source or a slot.
		if _, _, _, _, _, err := parseFragment(buf[:n]); err != nil || !u.local && !validPacketDestination(addr) {
			continue
		}
		key := packetAddrKey(addr)
		source, ok := u.source(key)
		if !ok {
			continue
		}
		u.mu.Lock()
		source.last = time.Now()
		received := source.received + n
		source.received = received
		u.mu.Unlock()
		body, err := source.partial.add(buf[:n], time.Now())
		if err != nil || len(body) == 0 {
			continue
		}
		u.mu.Lock()
		source.received = 0
		u.mu.Unlock()
		request, plain, err := decodeFrame(body)
		if err != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Profile != "" && request.Profile != "plain" || plain == nil {
			continue
		}
		var destination net.Addr
		budget := -1
		if u.local {
			destination = clonePacketAddr(u.reply)
			if u.reply == nil {
				destination = replyAddress(addr)
			}
			if destination == nil && request.Stream {
				continue
			}
		} else {
			destination = clonePacketAddr(addr)
			if !loopbackPacketAddr(destination) {
				budget = amplificationFactor * received
			}
		}
		activeKey := key + "\x00" + request.CorrelationID
		u.mu.Lock()
		if _, duplicate := u.active[activeKey]; duplicate {
			u.mu.Unlock()
			continue
		}
		select {
		case u.requests <- struct{}{}:
			u.active[activeKey] = struct{}{}
			source.active[activeKey] = struct{}{}
		default:
			u.mu.Unlock()
			continue
		}
		u.mu.Unlock()
		workers.Go(func() {
			defer func() {
				<-u.requests
				u.mu.Lock()
				delete(u.active, activeKey)
				delete(source.active, activeKey)
				u.mu.Unlock()
			}()
			if u.local {
				write := func(FrameResponse) error { return nil }
				if destination != nil {
					write = u.writer(ctx, conn, destination, budget)
				}
				runCorrelatedPlainRequest(ctx, write, modes, true, u.d, request, u.bound.Ingress(""), plain)
				return
			}
			timeout := 4 * time.Second
			if request.Stream {
				timeout = 20 * time.Second
			}
			requestCtx, finish := context.WithTimeout(ctx, timeout)
			defer finish()
			in := dispatch.Ingress{Source: u.d.Source(packetSourceAddr(destination))}
			runCorrelatedPlainRequest(requestCtx, u.writer(requestCtx, conn, destination, budget), modes, true, u.d, request, in, plain)
		})
	}
}

// replyAddress is a sender's own address when it can receive a reply: a
// named or abstract Unix datagram socket.
func replyAddress(addr net.Addr) net.Addr {
	if a, ok := addr.(*net.UnixAddr); ok && a != nil && a.Name != "" && len(a.Name) <= 108 {
		return &net.UnixAddr{Name: a.Name, Net: "unixgram"}
	}
	return nil
}

func (u *AuthorPackets) source(key string) (*packetSource, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	source := u.sources[key]
	if source == nil {
		select {
		case u.slots <- struct{}{}:
		default:
			return nil, false
		}
		source = &packetSource{last: time.Now(), partial: newDatagramAssembler(), active: make(map[string]struct{})}
		u.sources[key] = source
	}
	return source, true
}

func (u *AuthorPackets) reapSources(now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for key, source := range u.sources {
		if len(source.active) == 0 && now.Sub(source.last) >= packetSourceIdle {
			delete(u.sources, key)
			<-u.slots
		}
	}
}

func (u *AuthorPackets) releaseSources() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for key := range u.sources {
		delete(u.sources, key)
		<-u.slots
	}
}

// writer sends each plain result frame as datagram fragments to destination,
// within the reply budget (budget < 0: unbounded but for the per-request wire
// budget every packet carrier uses).
func (u *AuthorPackets) writer(ctx context.Context, conn net.PacketConn, destination net.Addr, budget int) func(FrameResponse) error {
	var wireBytes int
	return func(response FrameResponse) error {
		payload, err := encodeFrame(response)
		if err != nil || len(payload) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		fragments, err := splitFragments(payload)
		if err != nil {
			return store.ErrCapacity
		}
		wire := 0
		for _, fragment := range fragments {
			wire += len(fragment)
		}
		final := response.Event == "error" || response.Event == "end"
		limit := udpWireBudget - udpErrorReserve
		if final {
			limit = udpWireBudget
		}
		if budget >= 0 {
			limit = min(limit, budget)
			if final {
				limit = budget + amplificationReserve
			}
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
		deadline, _ := writeCtx.Deadline()
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer conn.SetWriteDeadline(time.Time{})
		for _, fragment := range fragments {
			if err := writePacketFragment(writeCtx, conn, fragment, destination); err != nil {
				return err
			}
		}
		wireBytes += wire
		return nil
	}
}

// packetSourceAddr is the address AIRC's per-source limits see: the IP of a
// UDP or ICMPv6 peer (nil, a listener label, for other carriers).
func packetSourceAddr(addr net.Addr) net.Addr {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a
	case *ICMPv6Addr:
		if a != nil {
			return &net.UDPAddr{IP: a.IP}
		}
	}
	return nil
}

func loopbackPacketAddr(addr net.Addr) bool {
	if a, ok := packetSourceAddr(addr).(*net.UDPAddr); ok && a != nil {
		return a.IP.IsLoopback()
	}
	return false
}
