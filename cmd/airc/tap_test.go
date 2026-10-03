package main

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// eventually polls condition until it holds. It is a real synchronisation
// point: the deadline is generous and only bounds a failure, never a success.
func eventually(t testing.TB, within time.Duration, what string, condition func() bool) {
	t.Helper()
	eventuallyEvery(t, within, 2*time.Millisecond, what, condition)
}

// eventuallyEvery is eventually with a caller-chosen polling interval, for
// conditions that cost a daemon round trip to evaluate.
func eventuallyEvery(t testing.TB, within, interval time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(interval)
	}
}

// tapProxy forwards TCP connections to a daemon while recording what the
// daemon has acknowledged, so a test can wait for "the blocking check is
// really subscribed" instead of sleeping. The daemon sends 765 only after the
// observation is registered under its lock, so a connection that has seen 765
// is guaranteed to receive every later live message.
type tapProxy struct {
	addr     string
	upstream string
	listener net.Listener

	mu          sync.Mutex
	observing   map[net.Conn]bool // open client connections acknowledged by 765
	failedDials int               // connections refused because the daemon was down
}

// tapDaemon returns a proxy in front of the daemon at upstream. Use tap.addr
// wherever the test would have used the daemon address.
func tapDaemon(t testing.TB, upstream string) *tapProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tapProxy{addr: listener.Addr().String(), upstream: upstream, listener: listener, observing: map[net.Conn]bool{}}
	var conns sync.WaitGroup
	closing := make(chan struct{})
	var active sync.Map
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			active.Store(client, struct{}{})
			conns.Add(1)
			go func() {
				defer conns.Done()
				defer active.Delete(client)
				p.forward(client, closing)
			}()
		}
	}()
	t.Cleanup(func() {
		close(closing)
		_ = listener.Close()
		active.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
		conns.Wait()
	})
	return p
}

func (p *tapProxy) forward(client net.Conn, closing <-chan struct{}) {
	defer client.Close()
	server, err := net.DialTimeout("tcp", p.upstream, 2*time.Second)
	if err != nil {
		p.mu.Lock()
		p.failedDials++
		p.mu.Unlock()
		return
	}
	defer server.Close()
	ended := false
	defer func() {
		p.mu.Lock()
		ended = true
		delete(p.observing, client)
		p.mu.Unlock()
	}()
	go func() {
		<-closing
		_ = server.Close()
	}()
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		reader := bufio.NewReader(server)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				if strings.HasPrefix(line, ":server 765 ") {
					p.mu.Lock()
					if !ended {
						p.observing[client] = true
					}
					p.mu.Unlock()
				}
				if _, werr := client.Write([]byte(line)); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 4096)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				if _, werr := server.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	<-done // either side ending tears down both through the deferred closes
}

func (p *tapProxy) observingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.observing)
}

func (p *tapProxy) refusedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failedDials
}

// waitObserving blocks until exactly n open connections have an acknowledged
// observation: for n=1 the blocking check under test is subscribed.
func (p *tapProxy) waitObserving(t testing.TB, n int) {
	t.Helper()
	eventually(t, 10*time.Second, "observing connections to reach the expected count", func() bool { return p.observingCount() == n })
}

// waitRefused blocks until the proxy has turned away n connections because the
// daemon was down, proving a client attempted to reconnect during the outage.
func (p *tapProxy) waitRefused(t testing.TB, n int) {
	t.Helper()
	eventually(t, 10*time.Second, "a reconnect attempt during the outage", func() bool { return p.refusedCount() >= n })
}
