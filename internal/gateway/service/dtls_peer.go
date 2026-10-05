package service

import (
	"io"
	"net"
	"os"
	"sync"
	"time"
)

const dtlsPeerQueueSize = 32

type dtlsPeer struct {
	listener      *dtlsPacketListener
	remote        *net.UDPAddr
	packets       chan []byte
	closed        chan struct{}
	closeOnce     sync.Once
	releaseOnce   sync.Once
	mu            sync.Mutex
	readWake      chan struct{}
	writeWake     chan struct{}
	readDeadline  time.Time
	writeDeadline time.Time
	lastActivity  time.Time
}

func (p *dtlsPeer) LocalAddr() net.Addr  { return p.listener.Addr() }
func (p *dtlsPeer) RemoteAddr() net.Addr { return p.remote }

func (p *dtlsPeer) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		select {
		case <-p.closed:
			return 0, nil, net.ErrClosed
		default:
		}
		p.mu.Lock()
		deadline := p.readDeadline
		wake := p.readWake
		p.mu.Unlock()
		var timer *time.Timer
		var timerC <-chan time.Time
		if !deadline.IsZero() {
			if !time.Now().Before(deadline) {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(time.Until(deadline))
			timerC = timer.C
		}
		select {
		case packet := <-p.packets:
			if timer != nil {
				timer.Stop()
			}
			select {
			case <-p.closed:
				return 0, nil, net.ErrClosed
			default:
			}
			if len(packet) > len(b) {
				return 0, nil, io.ErrShortBuffer
			}
			n := copy(b, packet)
			return n, p.remote, nil
		case <-p.closed:
			if timer != nil {
				timer.Stop()
			}
			return 0, nil, net.ErrClosed
		case <-wake:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-timerC:
			return 0, nil, os.ErrDeadlineExceeded
		}
	}
}

func (p *dtlsPeer) WriteTo(b []byte, addr net.Addr) (int, error) {
	if len(b) > dtlsPacketMax {
		return 0, io.ErrShortBuffer
	}
	if addr == nil || addr.String() != p.remote.String() {
		return 0, net.ErrClosed
	}
	select {
	case <-p.closed:
		return 0, net.ErrClosed
	default:
	}
	return p.listener.writePeer(p, b)
}

func (p *dtlsPeer) Close() error {
	p.listener.closePeer(p, time.Time{})
	return nil
}

// Only verified DTLS application I/O or a completed handshake renews idle time.
// A forged packet from the peer's address cannot retain an admission slot.
func (p *dtlsPeer) activity() {
	p.listener.mu.Lock()
	p.lastActivity = time.Now()
	p.listener.mu.Unlock()
}

func (p *dtlsPeer) SetDeadline(deadline time.Time) error {
	if err := p.SetReadDeadline(deadline); err != nil {
		return err
	}
	return p.SetWriteDeadline(deadline)
}

func (p *dtlsPeer) SetReadDeadline(deadline time.Time) error {
	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return net.ErrClosed
	default:
	}
	p.readDeadline = deadline
	close(p.readWake)
	p.readWake = make(chan struct{})
	p.mu.Unlock()
	return nil
}

func (p *dtlsPeer) SetWriteDeadline(deadline time.Time) error {
	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return net.ErrClosed
	default:
	}
	p.writeDeadline = deadline
	close(p.writeWake)
	p.writeWake = make(chan struct{})
	p.mu.Unlock()
	p.listener.peerWriteDeadlineChanged(p)
	return nil
}

func (p *dtlsPeer) release() {
	p.releaseOnce.Do(func() { <-p.listener.slots })
}

var _ net.PacketConn = (*dtlsPeer)(nil)
