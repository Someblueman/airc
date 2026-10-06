//go:build darwin || linux

package gateway_test

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The owned relay initially withholds all upstream replies. Switching enables
// forwarding only for later connections, preserving the configured endpoint.
type fileMailboxRelay struct {
	listener    net.Listener
	forward     atomic.Bool
	accepted    chan struct{}
	mu          sync.Mutex
	connections map[net.Conn]bool
	closed      bool
	workers     sync.WaitGroup
	once        sync.Once
}

func holdFileMailboxUpstream(t *testing.T, f *fileMailboxFixture) *fileMailboxRelay {
	t.Helper()
	upstream := f.config.Bindings[0].Address
	root, err := os.MkdirTemp("/tmp", "agw-mb-relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "u.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	r := &fileMailboxRelay{listener: listener, accepted: make(chan struct{}, 1), connections: map[net.Conn]bool{}}
	t.Cleanup(r.close)
	r.workers.Add(1)
	go func() {
		defer r.workers.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				_ = connection.Close()
				return
			}
			r.connections[connection] = true
			r.mu.Unlock()
			r.workers.Add(1)
			go func() {
				defer r.workers.Done()
				defer func() { _ = connection.Close(); r.mu.Lock(); delete(r.connections, connection); r.mu.Unlock() }()
				_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
				if !r.forward.Load() {
					select {
					case r.accepted <- struct{}{}:
					default:
					}
					_, _ = io.CopyN(io.Discard, connection, 16<<10)
					return
				}
				peer, err := net.DialTimeout("unix", upstream, time.Second)
				if err != nil {
					return
				}
				defer peer.Close()
				_ = peer.SetDeadline(time.Now().Add(15 * time.Second))
				r.mu.Lock()
				if r.closed {
					r.mu.Unlock()
					return
				}
				r.connections[peer] = true
				r.mu.Unlock()
				defer func() { r.mu.Lock(); delete(r.connections, peer); r.mu.Unlock() }()
				done := make(chan struct{})
				go func() { _, _ = io.CopyN(peer, connection, 1<<20); _ = peer.Close(); close(done) }()
				_, _ = io.CopyN(connection, peer, 1<<20)
				_ = connection.Close()
				_ = peer.Close()
				<-done
			}()
		}
	}()
	return r
}

func (r *fileMailboxRelay) close() {
	r.once.Do(func() {
		_ = r.listener.Close()
		r.mu.Lock()
		r.closed = true
		for connection := range r.connections {
			_ = connection.Close()
		}
		r.mu.Unlock()
		r.workers.Wait()
	})
}
