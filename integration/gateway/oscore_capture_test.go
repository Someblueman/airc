//go:build darwin || linux

package gateway_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

// The relay retains bounded packet snapshots and eight response timestamps.
// It forwards ciphertext unchanged, optionally holds request continuations, and
// joins both socket readers on close.
type oscoreCapture struct {
	front, back *net.UDPConn
	mu          sync.Mutex
	peer        *net.UDPAddr
	packet      []byte
	reply       []byte
	hold        bool
	received    bool
	pending     []byte
	responses   chan time.Time
	workers     sync.WaitGroup
	once        sync.Once
}

func captureOSCORE(t *testing.T, address string) *oscoreCapture {
	t.Helper()
	destination, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	front, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	back, err := net.DialUDP("udp", nil, destination)
	if err != nil {
		_ = front.Close()
		t.Fatal(err)
	}
	p := &oscoreCapture{front: front, back: back, responses: make(chan time.Time, 8)}
	t.Cleanup(p.close)
	p.workers.Add(2)
	go func() {
		defer p.workers.Done()
		data := make([]byte, 65536)
		for {
			n, peer, err := front.ReadFromUDP(data)
			if err != nil {
				return
			}
			decoded := message.Message{Options: make(message.Options, 0, 32)}
			_, decodeErr := coder.DefaultCoder.Decode(data[:n], &decoded)
			p.mu.Lock()
			p.peer = peer
			if decodeErr == nil && decoded.Code == codes.POST && decoded.Options.HasOption(message.OptionID(9)) && len(decoded.Payload) != 0 {
				p.packet = append(p.packet[:0], data[:n]...)
			}
			drop := p.hold && p.received && decodeErr == nil && decoded.Code >= codes.GET && decoded.Code <= codes.Code(7)
			if drop {
				p.pending = append(p.pending[:0], data[:n]...)
			}
			p.mu.Unlock()
			if drop {
				continue
			}
			if _, err := back.Write(data[:n]); err != nil {
				return
			}
		}
	}()
	go func() {
		defer p.workers.Done()
		data := make([]byte, 65536)
		for {
			n, err := back.Read(data)
			if err != nil {
				return
			}
			decoded := message.Message{Options: make(message.Options, 0, 32)}
			_, decodeErr := coder.DefaultCoder.Decode(data[:n], &decoded)
			p.mu.Lock()
			p.reply = append(p.reply[:0], data[:n]...)
			if p.hold && decodeErr == nil && decoded.Code == codes.Changed && (p.received || decoded.Options.HasOption(message.OptionID(9))) {
				p.received = true
				select {
				case p.responses <- time.Now():
				default:
				}
			}
			peer := p.peer
			p.mu.Unlock()
			if peer != nil {
				if _, err := front.WriteToUDP(data[:n], peer); err != nil {
					return
				}
			}
		}
	}()
	return p
}

func (p *oscoreCapture) close() {
	p.once.Do(func() { _ = p.front.Close(); _ = p.back.Close(); p.workers.Wait() })
}
func (p *oscoreCapture) latest() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.packet...)
}

func (p *oscoreCapture) holdResponse() { p.mu.Lock(); p.hold = true; p.mu.Unlock() }
func (p *oscoreCapture) continueResponse(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	packet := append([]byte(nil), p.pending...)
	p.pending = nil
	p.mu.Unlock()
	if len(packet) == 0 {
		t.Fatal("native client did not request a response continuation")
	}
	if _, err := p.back.Write(packet); err != nil {
		t.Fatal(err)
	}
}

func (p *oscoreCapture) latestReply() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.reply...)
}
