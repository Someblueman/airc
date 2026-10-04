package gateway_test

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// One owned lease only. The helper always joins both directions and noise work;
// it never changes the pinned daemon's listener or state.
func backendProxy(t *testing.T, upstream, mode string, secure *tls.Config) (string, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "agbp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	network, address := "unix", filepath.Join(root, "proxy.sock")
	if secure != nil {
		network, address = "tcp", "127.0.0.1:0"
	}
	l, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	if secure != nil {
		l = tls.NewListener(l, secure)
	}
	done := make(chan struct{})
	ready := make(chan struct{})
	var mu sync.Mutex
	var client, server net.Conn
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		client = c
		mu.Unlock()
		close(ready)
		defer c.Close()
		if mode == "stall" {
			_, _ = io.Copy(io.Discard, c)
			return
		}
		s, err := net.Dial("unix", upstream)
		if err != nil {
			return
		}
		mu.Lock()
		server = s
		mu.Unlock()
		defer s.Close()
		ended := make(chan struct{})
		var work sync.WaitGroup
		var output sync.Mutex
		write := func(line string) { output.Lock(); defer output.Unlock(); _, _ = io.WriteString(c, line+"\r\n") }
		work.Go(func() {
			defer s.Close()
			if secure != nil {
				// This owned TLS front door authenticates its independent access token;
				// the fixture's Unix listener has no PASS gate, so forward only SASL/chat.
				reader := bufio.NewReader(c)
				first, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(first) != "PASS :"+strings.Repeat("b", 64) {
					write(":proxy 464 * :invalid access credential")
					return
				}
				_, _ = io.Copy(s, reader)
			} else {
				_, _ = io.Copy(s, c)
			}
		})
		if mode == "withhold" {
			work.Go(func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ended:
						return
					case <-ticker.C:
						write(":proxy NOTICE fixture-author :unrelated traffic")
					}
				}
			})
		}
		scanner := bufio.NewScanner(s)
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			line := scanner.Text()
			if mode == "withhold" && strings.Contains(line, " 762 ") {
				continue
			}
			if mode == "features" {
				line = strings.ReplaceAll(line, "SAFE_RETRY=1", "SAFE_RETRY=0")
			}
			write(line)
		}
		close(ended)
		_ = c.Close()
		work.Wait()
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		if client != nil {
			_ = client.Close()
		}
		if server != nil {
			_ = server.Close()
		}
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("proxy work survived cleanup")
		}
	})
	return l.Addr().String(), done, ready
}
