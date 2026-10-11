package service

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/pion/dtls/v3/pkg/protocol"
	"github.com/pion/dtls/v3/pkg/protocol/recordlayer"
)

const dtlsPacketMax = 1200

// dtlsPeerIdle releases an association's slot once it has had no
// authenticated activity for this long. linkedDTLSPeerIdle is empty in
// release builds; see core.LinkedDuration.
var (
	dtlsPeerIdle       = core.LinkedDuration(linkedDTLSPeerIdle, 30*time.Second)
	linkedDTLSPeerIdle string
)

type dtlsPacketListener struct {
	udp          *net.UDPConn
	slots        chan struct{}
	done         chan struct{}
	shutdownDone chan struct{}
	accepted     chan *dtlsPeer
	closeOnce    sync.Once
	readWG       sync.WaitGroup
	mu           sync.Mutex
	peers        map[string]*dtlsPeer
	queued       map[*dtlsPeer]struct{}
	writeSlot    chan struct{}
	activeMu     sync.Mutex
	active       *dtlsPeer
	closeErr     error
}

func newDTLSPacketListener(address string, slots chan struct{}) (*dtlsPacketListener, error) {
	if slots == nil || cap(slots) < 1 {
		return nil, errors.New("invalid DTLS slots")
	}
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	udp, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	l := &dtlsPacketListener{udp: udp, slots: slots, done: make(chan struct{}), shutdownDone: make(chan struct{}), accepted: make(chan *dtlsPeer, cap(slots)), peers: make(map[string]*dtlsPeer), queued: make(map[*dtlsPeer]struct{}), writeSlot: make(chan struct{}, 1)}
	l.readWG.Add(1)
	go l.readLoop()
	l.readWG.Add(1)
	go l.reapIdle()
	return l, nil
}

func (l *dtlsPacketListener) Addr() net.Addr { return l.udp.LocalAddr() }

func (l *dtlsPacketListener) Accept() (net.PacketConn, net.Addr, error) {
	for {
		select {
		case p := <-l.accepted:
			l.mu.Lock()
			select {
			case <-l.done:
				l.mu.Unlock()
				p.release()
				return nil, nil, l.closedError()
			case <-p.closed:
				l.mu.Unlock()
				continue
			default:
			}
			delete(l.queued, p)
			l.mu.Unlock()
			return p, p.remote, nil
		case <-l.done:
			return nil, nil, l.closedError()
		}
	}
}

func (l *dtlsPacketListener) closedError() error {
	<-l.shutdownDone
	if l.closeErr != nil {
		return l.closeErr
	}
	return net.ErrClosed
}

func (l *dtlsPacketListener) readLoop() {
	defer l.readWG.Done()
	for {
		buf := make([]byte, dtlsPacketMax+1)
		n, addr, err := l.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			l.closeOnce.Do(func() { l.shutdown(err) })
			return
		}
		if n == 0 || n > dtlsPacketMax || !validDTLSPacket(buf[:n]) {
			continue
		}
		key := addr.String()
		l.mu.Lock()
		peer := l.peers[key]
		l.mu.Unlock()
		if peer == nil {
			if !validDTLSClientHello(buf[:n]) {
				continue
			}
			select {
			case l.slots <- struct{}{}:
			default:
				continue
			}
			peer = &dtlsPeer{listener: l, remote: &net.UDPAddr{IP: append(net.IP(nil), addr.IP...), Port: addr.Port, Zone: addr.Zone}, packets: make(chan []byte, dtlsPeerQueueSize), closed: make(chan struct{}), readWake: make(chan struct{}), writeWake: make(chan struct{})}
			peer.lastActivity = time.Now()
			l.mu.Lock()
			select {
			case <-l.done:
				l.mu.Unlock()
				peer.release()
				return
			default:
			}
			if existing := l.peers[key]; existing != nil {
				l.mu.Unlock()
				peer.release()
				peer = existing
			} else {
				l.peers[key] = peer
				l.queued[peer] = struct{}{}
				l.mu.Unlock()
				select {
				case l.accepted <- peer:
				default:
					_ = peer.Close()
					continue
				}
			}
		}
		select {
		case peer.packets <- append([]byte(nil), buf[:n]...):
		default:
		}
	}
}

func (l *dtlsPacketListener) reapIdle() {
	defer l.readWG.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case now := <-ticker.C:
			var idle []*dtlsPeer
			l.mu.Lock()
			for _, p := range l.peers {
				if !p.lastActivity.IsZero() && now.Sub(p.lastActivity) >= dtlsPeerIdle {
					idle = append(idle, p)
				}
			}
			l.mu.Unlock()
			for _, p := range idle {
				l.closePeer(p, now.Add(-dtlsPeerIdle))
			}
		}
	}
}

func validDTLSClientHello(packet []byte) bool {
	if !validDTLSPacket(packet) || len(packet) < recordlayer.FixedHeaderSize+12 {
		return false
	}
	records, err := recordlayer.UnpackDatagram(packet)
	if err != nil || len(records) == 0 {
		return false
	}
	var header recordlayer.Header
	if err := header.Unmarshal(records[0]); err != nil || header.ContentType != protocol.ContentTypeHandshake || header.Epoch != 0 {
		return false
	}
	payload := records[0][recordlayer.FixedHeaderSize:]
	return len(payload) >= 12 && payload[0] == 1
}

func validDTLSPacket(packet []byte) bool {
	if len(packet) < recordlayer.FixedHeaderSize || len(packet) > dtlsPacketMax {
		return false
	}
	_, err := recordlayer.UnpackDatagram(packet)
	return err == nil
}

func (l *dtlsPacketListener) closePeer(p *dtlsPeer, idleBefore time.Time) {
	l.mu.Lock()
	if !idleBefore.IsZero() && p.lastActivity.After(idleBefore) {
		l.mu.Unlock()
		return
	}
	p.closeOnce.Do(func() {
		close(p.closed)
		p.mu.Lock()
		close(p.readWake)
		p.readWake = make(chan struct{})
		close(p.writeWake)
		p.writeWake = make(chan struct{})
		p.mu.Unlock()
	})
	if current := l.peers[p.remote.String()]; current == p {
		delete(l.peers, p.remote.String())
	}
	_, queued := l.queued[p]
	delete(l.queued, p)
	l.mu.Unlock()
	if queued {
		p.release()
	}
	l.activeMu.Lock()
	if l.active == p {
		_ = l.udp.SetWriteDeadline(time.Now())
	}
	l.activeMu.Unlock()
}

func (l *dtlsPacketListener) peerWriteDeadlineChanged(p *dtlsPeer) {
	l.activeMu.Lock()
	if l.active == p {
		p.mu.Lock()
		deadline := p.writeDeadline
		p.mu.Unlock()
		select {
		case <-p.closed:
			deadline = time.Now()
		default:
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(3 * time.Second)
		}
		_ = l.udp.SetWriteDeadline(deadline)
	}
	l.activeMu.Unlock()
}

func (l *dtlsPacketListener) writePeer(p *dtlsPeer, data []byte) (int, error) {
	for {
		p.mu.Lock()
		deadline, wake := p.writeDeadline, p.writeWake
		p.mu.Unlock()
		if deadline.IsZero() {
			deadline = time.Now().Add(3 * time.Second)
		}
		var timer *time.Timer
		var timerC <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timerC = timer.C
		}
		select {
		case l.writeSlot <- struct{}{}:
			if timer != nil {
				timer.Stop()
			}
		case <-p.closed:
			if timer != nil {
				timer.Stop()
			}
			return 0, net.ErrClosed
		case <-l.done:
			if timer != nil {
				timer.Stop()
			}
			return 0, net.ErrClosed
		case <-wake:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-timerC:
			return 0, os.ErrDeadlineExceeded
		}
		select {
		case <-p.closed:
			<-l.writeSlot
			return 0, net.ErrClosed
		case <-l.done:
			<-l.writeSlot
			return 0, net.ErrClosed
		default:
		}
		l.activeMu.Lock()
		// Close and deadline updates share this guard with write admission. A
		// canceled peer must not install a future socket deadline afterward.
		select {
		case <-p.closed:
			l.activeMu.Unlock()
			<-l.writeSlot
			return 0, net.ErrClosed
		case <-l.done:
			l.activeMu.Unlock()
			<-l.writeSlot
			return 0, net.ErrClosed
		default:
		}
		p.mu.Lock()
		deadline = p.writeDeadline
		p.mu.Unlock()
		if deadline.IsZero() {
			deadline = time.Now().Add(3 * time.Second)
		}
		l.active = p
		deadlineErr := l.udp.SetWriteDeadline(deadline)
		l.activeMu.Unlock()
		n, err := 0, deadlineErr
		if err == nil {
			n, err = l.udp.WriteToUDP(data, p.remote)
		}
		l.activeMu.Lock()
		if l.active == p {
			l.active = nil
		}
		l.activeMu.Unlock()
		<-l.writeSlot
		return n, err
	}
}

func (l *dtlsPacketListener) Close() error {
	l.closeOnce.Do(func() { l.shutdown(nil) })
	l.readWG.Wait()
	return l.closeErr
}

func (l *dtlsPacketListener) shutdown(cause error) {
	// shutdown is always called through closeOnce from Close or a fatal reader
	// error. The reader path never waits for readWG, avoiding self-join.
	defer close(l.shutdownDone)
	if cause != nil {
		l.closeErr = cause
	}
	close(l.done)
	if err := l.udp.Close(); l.closeErr == nil {
		l.closeErr = err
	}
	l.mu.Lock()
	peers := make([]*dtlsPeer, 0, len(l.peers))
	for _, p := range l.peers {
		peers = append(peers, p)
	}
	l.mu.Unlock()
	for _, p := range peers {
		_ = p.Close()
	}
	for {
		select {
		case p := <-l.accepted:
			if p != nil {
				p.release()
			}
		default:
			return
		}
	}
}
