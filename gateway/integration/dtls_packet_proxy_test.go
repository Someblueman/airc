//go:build darwin || linux

package gateway_test

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// The proxy forwards original DTLS datagrams; it never decrypts application data.
type dtlsPacketProxy struct {
	conn                  *net.UDPConn
	remote                *net.UDPAddr
	client                *net.UDPAddr
	done                  chan struct{}
	closeOnce             sync.Once
	mu                    sync.Mutex
	err                   error
	dropClientHello       bool
	droppedClientHello    bool
	dropServerApplication bool
	droppedServerApp      int
	serverDrop            chan struct{}
	reorderClientApp      bool
	pendingClientApp      []byte
	reordered             int
	duplicated            int
}

func newDTLSPacketProxy(t *testing.T, remote string, dropHello bool) *dtlsPacketProxy {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", remote)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	p := &dtlsPacketProxy{conn: conn, remote: addr, done: make(chan struct{}), serverDrop: make(chan struct{}), dropClientHello: dropHello}
	go p.loop()
	t.Cleanup(func() {
		p.close()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.err != nil {
			t.Error(p.err)
		}
	})
	return p
}

func (p *dtlsPacketProxy) close() {
	p.closeOnce.Do(func() { _ = p.conn.Close() })
	<-p.done
}

func (p *dtlsPacketProxy) addr() string { return p.conn.LocalAddr().String() }

func sameUDP(a, b *net.UDPAddr) bool { return a.Port == b.Port && a.IP.Equal(b.IP) && a.Zone == b.Zone }

func (p *dtlsPacketProxy) loop() {
	defer close(p.done)
	var buffer [1201]byte
	for {
		n, from, err := p.conn.ReadFromUDP(buffer[:])
		if err != nil {
			return
		}
		if n < 13 || n > 1200 {
			continue
		}
		packet := append([]byte(nil), buffer[:n]...)
		p.mu.Lock()
		backend := sameUDP(from, p.remote)
		if !backend && p.client == nil {
			p.client = from
		}
		if !backend && !sameUDP(from, p.client) {
			p.mu.Unlock()
			continue
		}
		if !backend && len(packet) >= 25 && packet[0] == 22 && packet[13] == 1 && p.dropClientHello && !p.droppedClientHello {
			p.droppedClientHello = true
			p.mu.Unlock()
			continue
		}
		if backend && packet[0] == 23 && p.dropServerApplication {
			p.droppedServerApp++
			if p.droppedServerApp == 1 {
				close(p.serverDrop)
			}
			p.mu.Unlock()
			continue
		}
		var packets [][]byte
		target := p.remote
		if backend {
			target = p.client
			packets = [][]byte{packet}
		} else if packet[0] == 23 && p.reorderClientApp {
			if p.pendingClientApp == nil {
				p.pendingClientApp = packet
				p.mu.Unlock()
				continue
			}
			packets = [][]byte{packet, p.pendingClientApp, packet}
			p.pendingClientApp = nil
			p.reorderClientApp = false
			p.reordered++
			p.duplicated++
		} else {
			packets = [][]byte{packet}
		}
		p.mu.Unlock()
		if target == nil {
			continue
		}
		for _, data := range packets {
			_ = p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if _, err := p.conn.WriteToUDP(data, target); err != nil {
				p.mu.Lock()
				if !errors.Is(err, net.ErrClosed) {
					p.err = err
				}
				p.mu.Unlock()
				return
			}
		}
	}
}

func (p *dtlsPacketProxy) setDropServerApplication(drop bool) {
	p.mu.Lock()
	p.dropServerApplication = drop
	p.mu.Unlock()
}
func (p *dtlsPacketProxy) setReorderClientApplication() {
	p.mu.Lock()
	p.reorderClientApp = true
	p.mu.Unlock()
}
func (p *dtlsPacketProxy) stats() (bool, int, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.droppedClientHello, p.droppedServerApp, p.reordered, p.duplicated
}
