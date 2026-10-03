package main

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type agentProxy struct {
	address  string
	accepted atomic.Int64
	mu       sync.Mutex
	peers    map[net.Conn]bool
}

// A byte-stream proxy, including TLS records. Each read chunk is delayed by
// half the nominal RTT in each direction; this is a latency fixture, not netem.
func delayedAgentProxy(t testing.TB, upstream string, rtt time.Duration) *agentProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &agentProxy{address: l.Addr().String(), peers: map[net.Conn]bool{}}
	acceptedDone := make(chan struct{})
	var work sync.WaitGroup
	work.Add(1)
	go func() {
		defer work.Done()
		defer close(acceptedDone)
		for {
			front, err := l.Accept()
			if err != nil {
				return
			}
			back, err := net.Dial("tcp", upstream)
			if err != nil {
				front.Close()
				return
			}
			p.accepted.Add(1)
			p.mu.Lock()
			p.peers[front] = true
			p.peers[back] = true
			p.mu.Unlock()
			work.Add(1)
			go func() {
				defer work.Done()
				copyStream := func(dst, src net.Conn) {
					buf := make([]byte, 64<<10)
					for {
						n, err := src.Read(buf)
						if err != nil {
							return
						}
						if rtt > 0 {
							timer := time.NewTimer(rtt / 2)
							select {
							case <-ctx.Done():
								timer.Stop()
								return
							case <-timer.C:
							}
						}
						if _, err := dst.Write(buf[:n]); err != nil {
							return
						}
					}
				}
				done := make(chan struct{})
				go func() { copyStream(back, front); back.Close(); close(done) }()
				copyStream(front, back)
				front.Close()
				back.Close()
				<-done
				p.mu.Lock()
				delete(p.peers, front)
				delete(p.peers, back)
				p.mu.Unlock()
			}()
		}
	}()
	t.Cleanup(func() { cancel(); l.Close(); <-acceptedDone; p.drop(); work.Wait() })
	return p
}

func (p *agentProxy) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.peers {
		c.Close()
	}
}

func closeIdleAgentClients(a *mcpAdapter) {
	pool := a.connections()
	for range len(pool) {
		conn := <-pool
		conn.mu.Lock()
		if conn.idle != nil {
			conn.idle.Stop()
			conn.idle = nil
		}
		if conn.client != nil {
			conn.client.Close()
			conn.client = nil
		}
		conn.mu.Unlock()
		pool <- conn
	}
}

func agentCallOK(t testing.TB, a *mcpAdapter, args []string) time.Duration {
	t.Helper()
	start := time.Now()
	r, _, err := a.call(context.Background(), args, "", 10*time.Second, false)
	if err != nil || r != nil && r.IsError {
		t.Fatalf("agent call failed: %v %+v", err, r)
	}
	return time.Since(start)
}
