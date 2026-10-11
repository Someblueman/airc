package adapter

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// LocalAuthorPackets serves plain author requests on a local packet carrier:
// Unix datagrams, POSIX and System V message queues, shared memory and
// mapped files. Requests are plain frames (a contract request naming its
// author) split into AGD1 datagrams; replies are plain response frames in
// AGD1 datagrams, unsigned and unsealed. The carrier's owner-only boundary
// (socket mode, queue permissions, private directories) is the access gate,
// and AIRC sees listener:<id> as the source.
//
// Replies go to the listener's reply destination when one is configured
// (queues, shared memory, mapped files), else to the sender's own bound
// address (Unix datagrams). A sender without an address gets no reply; its
// requests still run, and receipt.get on any carrier reports them.
type LocalAuthorPackets struct {
	d         *dispatch.Dispatcher
	bound     *dispatch.BoundAuthor
	reply     net.Addr
	slots     chan struct{}
	requests  chan struct{}
	mu        sync.Mutex
	sources   map[string]*packetSource
	active    map[string]struct{}
	writeSlot chan struct{}
}

// NewLocalAuthorPackets builds the driver. reply is the fixed reply
// destination, or nil to answer each sender at its own address.
func NewLocalAuthorPackets(d *dispatch.Dispatcher, bound *dispatch.BoundAuthor, reply net.Addr, slots, requests chan struct{}) (*LocalAuthorPackets, error) {
	if d == nil || !d.AuthorAccess() || cap(slots) < 1 || cap(requests) < 1 || reply != nil && !validPacketDestination(reply) {
		return nil, store.ErrInvalid
	}
	return &LocalAuthorPackets{d: d, bound: bound, reply: clonePacketAddr(reply), slots: slots, requests: requests, sources: make(map[string]*packetSource), active: make(map[string]struct{}), writeSlot: make(chan struct{}, 1)}, nil
}

// Serve reads until ctx ends, then closes conn and joins admitted work.
func (u *LocalAuthorPackets) Serve(parent context.Context, conn net.PacketConn) error {
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
	mode := packetWatchMode(conn)
	buf := make([]byte, agd1DatagramBytes+1)
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
		if n == 0 || n >= len(buf) || n > agd1DatagramBytes {
			continue
		}
		if _, _, _, _, _, err := parseAGD1Datagram(buf[:n]); err != nil {
			continue
		}
		key := packetAddrKey(addr)
		source, ok := u.source(key)
		if !ok {
			continue
		}
		body, err := source.partial.add(buf[:n], time.Now())
		if err != nil || len(body) == 0 {
			continue
		}
		request, plain, err := decodeFrame(body)
		if err != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Profile != "plain" || plain == nil || request.Envelope != "" {
			continue
		}
		destination := u.reply
		if destination == nil {
			destination = replyAddress(addr)
		}
		if destination == nil && request.Stream {
			continue
		}
		activeKey := key + "\x00" + request.CorrelationID
		u.mu.Lock()
		source.last = time.Now()
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
			write := func(FrameResponse) error { return nil }
			if destination != nil {
				write = u.writer(ctx, conn, destination)
			}
			runCorrelatedPlainRequest(ctx, write, []string{"long_poll", mode}, true, u.d, request, u.bound.Ingress(""), plain)
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

func (u *LocalAuthorPackets) source(key string) (*packetSource, bool) {
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

func (u *LocalAuthorPackets) reapSources(now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for key, source := range u.sources {
		if len(source.active) == 0 && now.Sub(source.last) >= packetSourceIdle {
			delete(u.sources, key)
			<-u.slots
		}
	}
}

func (u *LocalAuthorPackets) releaseSources() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for key := range u.sources {
		delete(u.sources, key)
		<-u.slots
	}
}

// writer sends each response frame as AGD1 datagrams to destination, within
// the per-request wire budget every packet carrier uses.
func (u *LocalAuthorPackets) writer(ctx context.Context, conn net.PacketConn, destination net.Addr) func(FrameResponse) error {
	var wireBytes int
	return func(response FrameResponse) error {
		payload, err := json.Marshal(response)
		if err != nil || len(payload) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		fragments, err := fragmentAGD1(payload)
		if err != nil {
			return store.ErrCapacity
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
		deadline, _ := writeCtx.Deadline()
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer conn.SetWriteDeadline(time.Time{})
		for _, fragment := range fragments {
			if err := writePacketFragment(writeCtx, conn, fragment, clonePacketAddr(destination)); err != nil {
				return err
			}
		}
		wireBytes += wire
		return nil
	}
}
