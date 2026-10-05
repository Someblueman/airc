//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

// udpPacketProxy forwards encrypted response datagrams without inspecting
// their payload. It only reads the AGD1 header to exercise loss and ordering.
type udpPacketProxy struct {
	conn              *net.UDPConn
	mailbox           *net.UDPAddr
	done              chan struct{}
	closeOnce         sync.Once
	mu                sync.Mutex
	err               error
	firstID           [16]byte
	haveFirst         bool
	dropped           int
	dropDone          chan struct{}
	reorder           bool
	pendingID         [16]byte
	pending           [][]byte
	reordered         int
	duplicated        int
	duplicateOriginal []byte
	duplicateWire     []byte
	pendingTotal      int
	pendingCount      int
	pendingDeadline   time.Time
	pendingByIndex    map[int][]byte
}

func newUDPPacketProxy(t *testing.T, mailbox *net.UDPConn) *udpPacketProxy {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	p := &udpPacketProxy{conn: conn, mailbox: mailbox.LocalAddr().(*net.UDPAddr), done: make(chan struct{}), dropDone: make(chan struct{})}
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

func (p *udpPacketProxy) close() {
	p.closeOnce.Do(func() { _ = p.conn.Close() })
	<-p.done
}

func (p *udpPacketProxy) setReorderNext() {
	p.mu.Lock()
	p.reorder = true
	p.mu.Unlock()
}

func (p *udpPacketProxy) stats() (int, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dropped, p.reordered, p.duplicated
}

func (p *udpPacketProxy) duplicateBytes() ([]byte, []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.duplicateOriginal...), append([]byte(nil), p.duplicateWire...)
}

func (p *udpPacketProxy) loop() {
	defer close(p.done)
	var buf [1001]byte
	for {
		if err := p.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			p.recordWriteError(err)
			return
		}
		n, _, err := p.conn.ReadFromUDP(buf[:])
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				p.mu.Lock()
				if p.pending != nil && time.Now().After(p.pendingDeadline) {
					p.pending = nil
					p.pendingByIndex = nil
				}
				p.mu.Unlock()
				continue
			}
			if !errors.Is(err, net.ErrClosed) {
				p.mu.Lock()
				p.err = err
				p.mu.Unlock()
			}
			return
		}
		if n > 1000 {
			continue
		}
		if n < 28 || string(buf[:4]) != "AGD1" {
			continue
		}
		packet := append([]byte(nil), buf[:n]...)
		var id [16]byte
		copy(id[:], packet[4:20])
		count := int(binary.BigEndian.Uint16(packet[26:28]))
		total := int(binary.BigEndian.Uint32(packet[20:24]))
		index := int(binary.BigEndian.Uint16(packet[24:26]))
		if total < 1 || total > 64<<10 || count < 1 || count != (total+971)/972 || index >= count || len(packet) < 28 {
			continue
		}
		want := 972
		if index == count-1 {
			want = total - 972*(count-1)
		}
		if len(packet)-28 != want {
			continue
		}
		p.mu.Lock()
		if !p.haveFirst {
			p.firstID, p.haveFirst = id, true
		}
		if id == p.firstID {
			p.dropped++
			if p.dropped == 1 {
				close(p.dropDone)
			}
			p.mu.Unlock()
			continue
		}
		if p.reorder {
			if p.pending == nil {
				p.pendingID = id
				p.pendingTotal = total
				p.pendingCount = count
				p.pendingDeadline = time.Now().Add(3 * time.Second)
				p.pendingByIndex = make(map[int][]byte, count)
			}
			if id != p.pendingID || total != p.pendingTotal || count != p.pendingCount || time.Now().After(p.pendingDeadline) {
				p.pending = nil
				p.mu.Unlock()
				continue
			}
			if prior, exists := p.pendingByIndex[index]; exists {
				if !bytes.Equal(prior, packet) {
					p.pending = nil
					p.pendingByIndex = nil
					p.reorder = false
				}
				p.mu.Unlock()
				continue
			}
			p.pendingByIndex[index] = packet
			p.pending = append(p.pending, packet)
			if len(p.pendingByIndex) < count {
				p.mu.Unlock()
				continue
			}
			packets := append([][]byte(nil), p.pending...)
			p.pending = nil
			p.pendingByIndex = nil
			p.pendingTotal = 0
			p.pendingCount = 0
			p.pendingDeadline = time.Time{}
			p.reorder = false
			p.reordered++
			p.duplicated++
			duplicate := append([]byte(nil), packets[0]...)
			p.duplicateOriginal = append([]byte(nil), packets[0]...)
			p.duplicateWire = append([]byte(nil), duplicate...)
			p.mu.Unlock()
			for i := len(packets) - 1; i >= 0; i-- {
				if err := p.write(packets[i]); err != nil {
					p.recordWriteError(err)
					return
				}
			}
			if err := p.write(duplicate); err != nil {
				p.recordWriteError(err)
				return
			}
			continue
		}
		p.mu.Unlock()
		if err := p.write(packet); err != nil {
			p.recordWriteError(err)
			return
		}
	}
}

func (p *udpPacketProxy) recordWriteError(err error) {
	if errors.Is(err, net.ErrClosed) {
		return
	}
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

func (p *udpPacketProxy) write(packet []byte) error {
	if err := p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, err := p.conn.WriteToUDP(packet, p.mailbox)
	return err
}

func writeUDPTokenFrame(t *testing.T, f udpFixture, correlation, token string) {
	t.Helper()
	writeUDPFrame(t, f, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
}

func writeUDPTokenFrameControlled(t *testing.T, f udpFixture, correlation, token string) (int, bool) {
	t.Helper()
	fragments := udpTokenFragments(t, correlation, token)
	if len(fragments) < 2 {
		t.Fatalf("controlled request did not fragment: %d", len(fragments))
	}
	for left, right := 0, len(fragments)-1; left < right; left, right = left+1, right-1 {
		fragments[left], fragments[right] = fragments[right], fragments[left]
	}
	destination := mustUDPAddress(t, f.address)
	deadline := time.Now().Add(3 * time.Second)
	for i, fragment := range fragments {
		if err := f.source.SetWriteDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		if _, err := f.source.WriteToUDP(fragment, destination); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := f.source.WriteToUDP(append([]byte(nil), fragment...), destination); err != nil {
				t.Fatal(err)
			}
		}
	}
	return len(fragments), true
}

func writeUDPTokenFramePartial(t *testing.T, f udpFixture, correlation, token string) int {
	t.Helper()
	fragments := udpTokenFragments(t, correlation, token)
	if len(fragments) < 2 {
		t.Fatalf("partial request did not fragment: %d", len(fragments))
	}
	if err := f.source.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.WriteToUDP(fragments[0], mustUDPAddress(t, f.address)); err != nil {
		t.Fatal(err)
	}
	return len(fragments)
}

func udpTokenFragments(t *testing.T, correlation, token string) [][]byte {
	t.Helper()
	body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("UDP controlled frame: %v bytes=%d", err, len(body))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return webTransportDatagrams(body, id)
}

func mustUDPAddress(t *testing.T, address string) *net.UDPAddr {
	t.Helper()
	parsed, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func expectUDPProxyDrop(t *testing.T, f udpFixture) {
	t.Helper()
	if err := f.mailbox.SetReadDeadline(time.Now().Add(700 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.mailbox.ReadFromUDP(make([]byte, 1001))
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("lost UDP response read=%v", err)
	}
}
