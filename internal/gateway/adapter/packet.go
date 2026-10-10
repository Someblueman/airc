package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const (
	packetSourceIdle = 5 * time.Second
	udpErrorReserve  = 4 << 10
	udpWireBudget    = 1 << 20
)

// PacketReturnRoute fixes the authenticated principal and binding to one
// encrypted response destination. The source address is never used as
// authority or as a response destination.
type PacketReturnRoute struct {
	ID          string
	Principal   string
	Binding     string
	RecipientID string
	Destination net.Addr
	Recipient   *ecdsa.PublicKey
}

type packetSource struct {
	last    time.Time
	partial *datagramAssembler
	active  map[string]struct{}
}

// PacketDatagrams owns one bounded packet ingress socket. It deliberately does not
// implement a PacketConn: each authenticated request is routed to its fixed
// owner-provisioned return destination.
type PacketDatagrams struct {
	network   string
	d         *dispatch.Dispatcher
	sealer    *auth.ResponseSealer
	realm     string
	routes    map[string]PacketReturnRoute
	slots     chan struct{}
	requests  chan struct{}
	mu        sync.Mutex
	sources   map[string]*packetSource
	active    map[string]struct{}
	writeSlot chan struct{}
}

func NewPacketDatagrams(d *dispatch.Dispatcher, sealer *auth.ResponseSealer, realm string, routes []PacketReturnRoute, slots, requests chan struct{}) (*PacketDatagrams, error) {
	if d == nil || sealer == nil || !validUDPID(realm) || cap(slots) < 1 || cap(requests) < 1 {
		return nil, store.ErrInvalid
	}
	u := &PacketDatagrams{d: d, sealer: sealer, realm: realm, routes: make(map[string]PacketReturnRoute), slots: slots, requests: requests, sources: make(map[string]*packetSource), active: make(map[string]struct{}), writeSlot: make(chan struct{}, 1)}
	principals := make(map[string]struct{})
	for _, route := range routes {
		if !validPacketRoute(route) || u.routes[route.Principal+"\x00"+route.Binding] != (PacketReturnRoute{}) {
			return nil, store.ErrInvalid
		}
		if _, exists := principals[route.Principal]; exists {
			return nil, store.ErrInvalid
		}
		principals[route.Principal] = struct{}{}
		if u.network == "" {
			u.network = route.Destination.Network()
		}
		if route.Destination.Network() != u.network {
			return nil, store.ErrInvalid
		}
		route.Recipient = cloneUDPPublicKey(route.Recipient)
		if route.Recipient == nil {
			return nil, store.ErrInvalid
		}
		route.Destination = clonePacketAddr(route.Destination)
		u.routes[route.Principal+"\x00"+route.Binding] = route
	}
	if len(u.routes) == 0 {
		return nil, store.ErrInvalid
	}
	return u, nil
}

func (u *PacketDatagrams) Serve(parent context.Context, conn net.PacketConn) error {
	if u == nil || conn == nil || conn.LocalAddr() == nil || conn.LocalAddr().Network() != u.network {
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
		// Reject malformed AGD1 before allocating a source or consuming the
		// shared admission slot.
		if _, _, _, _, _, err := parseAGD1Datagram(buf[:n]); err != nil {
			continue
		}
		source, ok := u.source(addr)
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
		if session, ok := conn.(*dccpSession); ok {
			session.idle = time.Now().Add(30 * time.Second)
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
			runCorrelatedVerifiedRequest(requestCtx, write, []string{"long_poll", packetWatchMode(conn)}, true, u.d, request, verified)
		})
	}
}

func (u *PacketDatagrams) source(addr net.Addr) (*packetSource, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	key := packetAddrKey(addr)
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

func (u *PacketDatagrams) reapSources(now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for addr, source := range u.sources {
		if len(source.active) == 0 && now.Sub(source.last) >= packetSourceIdle {
			delete(u.sources, addr)
			<-u.slots
		}
	}
}

func (u *PacketDatagrams) releaseSources() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for addr := range u.sources {
		delete(u.sources, addr)
		<-u.slots
	}
}

func (u *PacketDatagrams) writer(ctx context.Context, conn net.PacketConn, route PacketReturnRoute, verified dispatch.VerifiedMessage) func(FrameResponse) error {
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
			if _, ok := conn.(*CoAPUDPPackets); ok {
				wire += coapUDPMailboxOverhead
			}
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
		destination := clonePacketAddr(route.Destination)
		for _, fragment := range fragments {
			if writeCtx.Err() != nil {
				return writeCtx.Err()
			}
			if err := writePacketFragment(writeCtx, conn, fragment, destination); err != nil {
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

func validPacketRoute(route PacketReturnRoute) bool {
	for _, id := range []string{route.ID, route.Principal, route.Binding, route.RecipientID} {
		if !validUDPID(id) {
			return false
		}
	}
	if !validPacketDestination(route.Destination) || route.Recipient == nil || route.Recipient.Curve != elliptic.P256() {
		return false
	}
	_, err := route.Recipient.ECDH()
	return err == nil
}

func validPacketDestination(addr net.Addr) bool {
	switch a := addr.(type) {
	case *CANISOTPAddr:
		return validCANISOTPAddr(a)
	case *DCCPAddr:
		return validDCCPAddr(a)
	case *SystemVQueueAddr:
		return a != nil && a.Key > 0
	case *POSIXQueueAddr:
		return a != nil && ValidPOSIXQueueName(a.Name)
	case *SharedMemoryAddr:
		return a != nil && ValidSharedMemoryName(a.Name)
	case *MappedFileAddr:
		return a != nil && ValidMappedFileDirectory(a.Directory)
	case *ICMPv6Addr:
		return validICMPv6Addr(a)
	case *ICMPv4Addr:
		return validICMPv4Addr(a)
	case *RawIPAddr:
		return validRawIPAddr(a)
	case *RawEthernetAddr:
		return validRawEthernetAddr(a)
	case *net.UDPAddr:
		if a == nil || a.Port <= 0 || a.Port > 65535 || a.IP == nil || a.IP.To16() == nil || a.IP.IsUnspecified() || a.IP.IsMulticast() || a.IP.Equal(net.IPv4bcast) {
			return false
		}
		return true
	case *net.UnixAddr:
		return a != nil && a.Net == "unixgram" && filepath.IsAbs(a.Name) && filepath.Clean(a.Name) == a.Name && len(a.Name) <= 108
	default:
		return false
	}
}

func clonePacketAddr(addr net.Addr) net.Addr {
	switch a := addr.(type) {
	case *CANISOTPAddr:
		if a == nil {
			return (*CANISOTPAddr)(nil)
		}
		copy := *a
		return &copy
	case *DCCPAddr:
		if a == nil {
			return (*DCCPAddr)(nil)
		}
		return &DCCPAddr{IP: append(net.IP(nil), a.IP...), Port: a.Port}
	case *SystemVQueueAddr:
		if a == nil {
			return (*SystemVQueueAddr)(nil)
		}
		return &SystemVQueueAddr{Key: a.Key}
	case *POSIXQueueAddr:
		if a == nil {
			return (*POSIXQueueAddr)(nil)
		}
		return &POSIXQueueAddr{Name: a.Name}
	case *SharedMemoryAddr:
		if a == nil {
			return (*SharedMemoryAddr)(nil)
		}
		return &SharedMemoryAddr{Name: a.Name}
	case *MappedFileAddr:
		if a == nil {
			return (*MappedFileAddr)(nil)
		}
		return &MappedFileAddr{Directory: a.Directory}
	case *ICMPv6Addr:
		if a == nil {
			return (*ICMPv6Addr)(nil)
		}
		return &ICMPv6Addr{IP: append(net.IP(nil), a.IP...), Channel: a.Channel}
	case *ICMPv4Addr:
		if a == nil {
			return (*ICMPv4Addr)(nil)
		}
		return &ICMPv4Addr{IP: append(net.IP(nil), a.IP...), Channel: a.Channel}
	case *RawIPAddr:
		if a == nil {
			return (*RawIPAddr)(nil)
		}
		return &RawIPAddr{IP: append(net.IP(nil), a.IP...), Channel: a.Channel}
	case *RawEthernetAddr:
		if a == nil {
			return (*RawEthernetAddr)(nil)
		}
		return &RawEthernetAddr{MAC: append(net.HardwareAddr(nil), a.MAC...), Channel: a.Channel}
	case *net.UDPAddr:
		if a == nil {
			return (*net.UDPAddr)(nil)
		}
		return &net.UDPAddr{IP: append(net.IP(nil), a.IP...), Port: a.Port, Zone: a.Zone}
	case *net.UnixAddr:
		if a == nil {
			return (*net.UnixAddr)(nil)
		}
		return &net.UnixAddr{Name: a.Name, Net: a.Net}
	default:
		return nil
	}
}

func packetAddrKey(addr net.Addr) string {
	if addr == nil {
		return "unnamed:"
	}
	switch a := addr.(type) {
	case *net.UDPAddr:
		if a == nil {
			return "unnamed:udp"
		}
		return "udp:" + a.String()
	case *net.UnixAddr:
		if a == nil {
			return "unnamed:unix"
		}
		return "unix:" + a.String()
	default:
		return "other:" + addr.String()
	}
}

func packetWatchMode(conn net.PacketConn) string {
	if conn != nil && conn.LocalAddr() != nil {
		switch conn.LocalAddr().Network() {
		case "can_isotp", "canfd_isotp":
			return conn.LocalAddr().Network() + "_mailbox_datagrams"
		}
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "dccp" {
		return "dccp_connected_datagrams"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "mapped_file" {
		return "mapped_file_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "shared_memory" {
		return "shared_memory_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "system_v_queue" {
		return "system_v_queue_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "posix_mqueue" {
		return "posix_queue_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "icmp_v6" {
		return "icmp_v6_mailbox_datagrams"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "icmp_v4" {
		return "icmp_v4_mailbox_datagrams"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "raw_ip" {
		return "raw_ip_mailbox_datagrams"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "raw_ethernet" {
		return "raw_ethernet_mailbox_datagrams"
	}
	if _, ok := conn.(*CoAPUDPPackets); ok {
		return "coap_udp_mailbox"
	}
	if _, ok := conn.(*IPv4BroadcastPackets); ok {
		return "ipv4_broadcast_unicast_mailbox_datagrams"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "unixgram" {
		return "unixgram_mailbox_datagrams"
	}
	return "udp_mailbox_datagrams"
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
