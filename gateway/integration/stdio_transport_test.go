//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type stdioProcess struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  bytes.Buffer
	mu      sync.Mutex
	waitErr error
	waited  bool
	done    chan struct{}
}

func stdioFixtureConfig(t *testing.T) (string, config.Config, *detachedCredentials, *irc.Client) {
	t.Helper()
	socket := fixture(t)
	observer := dial(t, socket, "stdio-conformance-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "stdio-conformance-observer" })
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "stdio", Protocol: "stdio", Enabled: true, Operations: dispatch.ImplementedOperations()}}
	return socket, c, credentials, observer
}

func startStdio(t *testing.T, binary string, c config.Config) *stdioProcess {
	t.Helper()
	root := filepath.Dir(c.StateDir)
	path := filepath.Join(root, "stdio.json")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p := &stdioProcess{command: exec.Command(binary, "serve-stdio", "--file", path), done: make(chan struct{})}
	p.command.Env = append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+root, "XDG_STATE_HOME="+root)
	p.command.Stderr = &p.stderr
	p.stdin, err = p.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.stdout, err = p.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.mu.Lock()
		waited := p.waited
		p.mu.Unlock()
		if waited {
			select {
			case <-p.done:
				_ = p.stdout.Close()
				return
			default:
				if p.command.Process != nil {
					_ = p.command.Process.Kill()
				}
			}
			_ = p.wait()
			_ = p.stdout.Close()
			return
		}
		_ = p.stdin.Close()
		if p.command.Process != nil {
			_ = p.command.Process.Kill()
		}
		_ = p.wait()
		_ = p.stdout.Close()
	})
	return p
}

func (p *stdioProcess) write(t *testing.T, request frameRequest) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > adapter.MaxRequestFrameBytes {
		t.Fatal("request frame exceeds adapter bound")
	}
	if err := p.stdin.(*os.File).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if _, err := p.stdin.Write(append(header[:], body...)); err != nil {
		t.Fatal(err)
	}
}

func (p *stdioProcess) read(t *testing.T) adapter.FrameResponse {
	t.Helper()
	if err := p.stdout.(*os.File).SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	if _, err := io.ReadFull(p.stdout, header[:]); err != nil {
		t.Fatal(err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > adapter.MaxResponseFrameBytes {
		t.Fatalf("invalid stdio response size %d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(p.stdout, body); err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func (p *stdioProcess) wait() error {
	p.mu.Lock()
	if p.waited {
		p.mu.Unlock()
		<-p.done
		p.mu.Lock()
		err := p.waitErr
		p.mu.Unlock()
		return err
	}
	p.waited = true
	p.mu.Unlock()
	watchdog := time.AfterFunc(8*time.Second, func() { _ = p.command.Process.Kill() })
	err := p.command.Wait()
	watchdog.Stop()
	p.mu.Lock()
	p.waitErr = err
	close(p.done)
	p.mu.Unlock()
	return err
}
