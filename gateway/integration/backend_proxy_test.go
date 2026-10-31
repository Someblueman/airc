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
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testfs"
)

// One owned lease only. The helper always joins both directions and noise work;
// it never changes the native daemon's listener or state.
func backendProxy(t *testing.T, upstream, mode string, secure *tls.Config) (string, <-chan struct{}, <-chan struct{}, *atomic.Int64) {
	t.Helper()
	root, err := testfs.MkdirTemp("agbp-")
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
	delayedAck := mode == "watch-duplicate" || mode == "watch-preack" || mode == "watch-noack" || mode == "watch-rejectack"
	done := make(chan struct{})
	ready := make(chan struct{})
	replies := &atomic.Int64{}
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
		if mode != "watch" && mode != "watch-overlap" && mode != "watch-noise" && !delayedAck {
			close(ready)
		}
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
			scanner := bufio.NewScanner(c)
			scanner.Buffer(make([]byte, 4096), 65536)
			if secure != nil {
				// Authenticate the owned relay's independent access token before SASL/chat.
				if !scanner.Scan() {
					return
				}
				if scanner.Text() != "PASS :"+strings.Repeat("b", 64) {
					write(":proxy 464 * :invalid access credential")
					return
				}
			}
			for scanner.Scan() {
				line := scanner.Text()
				fields := strings.Fields(line)
				if len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
					fields = fields[1:]
				}
				if len(fields) > 0 && (fields[0] == "REPLY" || fields[0] == "REACT") {
					replies.Add(1)
				}
				if len(fields) > 0 && ((mode == "withhold-history" || mode == "watch" || mode == "watch-overlap" || mode == "watch-noise" || delayedAck) && fields[0] == "CHECK" || mode == "withhold-search" && fields[0] == "SEARCH") {
					replies.Add(1)
				}
				if mode == "watch-no-check" && len(fields) > 0 && (fields[0] == "OBSERVE" || fields[0] == "CHECK") {
					replies.Add(1)
				}
				if len(fields) > 0 && fields[0] == "DIRECTORY" {
					replies.Add(1)
				}
				if _, err := io.WriteString(s, line+"\r\n"); err != nil {
					return
				}
			}
		})
		if mode == "withhold" || mode == "withhold-parent" || mode == "withhold-history" || mode == "withhold-search" || mode == "withhold-directory" || mode == "watch-noise" {
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
		var held string
		for scanner.Scan() {
			line := scanner.Text()
			if mode == "withhold-history" && (strings.Contains(line, " 784 ") || strings.Contains(line, " 785 ")) {
				continue
			}
			if mode == "withhold-search" && (strings.Contains(line, " 760 ") || strings.Contains(line, " 761 ")) {
				continue
			}
			if mode == "withhold-directory" && (strings.Contains(line, " 773 ") || strings.Contains(line, " 774 ")) {
				continue
			}
			if mode == "directory-feature-zero" {
				line = withdraw(line, "DIRECTORY")
			}
			if mode == "directory-empty" && strings.Contains(line, " 773 ") {
				continue
			}
			if mode == "directory-wrong-nick" && strings.Contains(line, " 773 ") {
				line = strings.ReplaceAll(line, `"nick":"fixture-author"`, `"nick":"unrelated-agent"`)
			}
			if mode == "directory-malformed" && strings.Contains(line, " 773 ") {
				line = ":proxy 773 fixture-author :{"
			}
			if mode == "directory-duplicate" && strings.Contains(line, " 773 ") {
				write(line)
			}
			if mode == "withhold-parent" && (strings.Contains(line, " 777 ") || strings.Contains(line, " 778 ")) {
				continue
			}
			if mode == "withhold" && strings.Contains(line, " 762 ") {
				continue
			}
			if mode == "wrong-root" && strings.Contains(line, " 762 ") {
				const field = "\"thread_id\":\""
				if start := strings.Index(line, field); start >= 0 {
					start += len(field)
					if len(line) >= start+32 {
						line = line[:start] + strings.Repeat("e", 32) + line[start+32:]
					}
				}
			}
			if mode == "watch-no-check" {
				line = withdraw(line, "CHECK")
			}
			if mode == "features" {
				line = withdraw(line, "SAFE_RETRY")
			}
			if mode == "reaction-feature-zero" {
				line = withdraw(line, "REACTIONS")
			}
			if mode == "custom-reaction-feature-zero" {
				line = withdraw(line, "CUSTOM_REACTIONS")
			}
			if mode == "watch-overlap" && strings.Contains(line, " 785 ") {
				held = line
				close(ready)
				continue
			}
			if delayedAck && strings.Contains(line, " 765 ") {
				held = line
				close(ready)
				continue
			}
			if mode == "watch-duplicate" && held != "" && strings.Contains(line, " PRIVMSG ") {
				write(held)
				held = ""
			}
			write(line)
			if (mode == "watch" || mode == "watch-noise") && strings.Contains(line, " 785 ") {
				close(ready)
			}
			if delayedAck && held != "" && strings.Contains(line, " PRIVMSG ") {
				if mode == "watch-preack" {
					write(held)
				}
				if mode == "watch-rejectack" {
					write(":proxy 403 fixture-author #gateway-fixture :observation denied")
				}
				held = ""
			}
			if held != "" && strings.Contains(line, " PRIVMSG ") {
				write(held)
				held = ""
			}
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
	return l.Addr().String(), done, ready, replies
}

// withdraw takes feature away from a 005 line: the daemon's AIRC level grants
// every version-only feature, so a lacking one is said with an explicit
// FEATURE=0 token, which a client reads over the level; a token of its own
// (SAFE_RETRY=1) is replaced the same way.
func withdraw(line, feature string) string {
	if !strings.Contains(line, " 005 ") {
		return line
	}
	line = strings.ReplaceAll(line, " "+feature+"=1", "")
	if i := strings.LastIndex(line, " :"); i >= 0 {
		return line[:i] + " " + feature + "=0" + line[i:]
	}
	return line + " " + feature + "=0"
}
