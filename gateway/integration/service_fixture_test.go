//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/internal/testwait"
)

type gatewayProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
	stderr  processOutput
	ready   chan []byte
	// passes are the polled mailbox passes the gateway traced on stderr
	// (AIRC_GATEWAY_MAILBOX_TRACE), for waits on the poller having looked.
	passes struct {
		sync.Mutex
		began []time.Time
	}
}

// processOutput collects a child's stderr while a goroutine reads it and
// the test reads it back.
type processOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *processOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *processOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// mailboxPassAfter waits for the gateway's polled mailbox to complete a pass
// that began after since: one that saw what the test had put in place by
// then. The gateway traces each pass as a JSON line on stderr.
func (p *gatewayProcess) mailboxPassAfter(t *testing.T, since time.Time, what string) {
	t.Helper()
	testwait.Every(t, 5*time.Second, 10*time.Millisecond, what, func() bool {
		select {
		case <-p.done:
			t.Fatal("gateway exited while waiting for", what, p.err, p.stderr.String())
		default:
		}
		p.passes.Lock()
		defer p.passes.Unlock()
		return slices.ContainsFunc(p.passes.began, func(began time.Time) bool { return began.After(since) })
	})
}

// readStderr copies the child's stderr into p.stderr, noting each traced
// mailbox pass.
func (p *gatewayProcess) readStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = p.stderr.Write(append(append([]byte(nil), line...), '\n'))
		var pass struct {
			Listener string    `json:"listener"`
			Began    time.Time `json:"began"`
		}
		if json.Unmarshal(line, &pass) == nil && pass.Listener != "" {
			p.passes.Lock()
			p.passes.began = append(p.passes.began, pass.Began)
			p.passes.Unlock()
		}
	}
}

func gatewayBinary(t *testing.T) string {
	t.Helper()
	return currentGatewayBinary(t)
}

func startGateway(t *testing.T, binary string, c config.Config) *gatewayProcess {
	t.Helper()
	root := filepath.Dir(c.StateDir)
	path := filepath.Join(root, "gateway.json")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p := &gatewayProcess{done: make(chan struct{}), ready: make(chan []byte, 1)}
	p.command = exec.Command(binary, "serve", "--file", path)
	p.command.Env = append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+root, "XDG_STATE_HOME="+root, "AIRC_GATEWAY_MAILBOX_TRACE=1")
	stdout, err := p.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := p.command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Logf("owned gateway pid=%d", p.command.Process.Pid)
	var readDone sync.WaitGroup
	readDone.Go(func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			p.ready <- append([]byte(nil), scanner.Bytes()...)
		}
		for scanner.Scan() {
		}
	})
	readDone.Go(func() { p.readStderr(stderr) })
	go func() { readDone.Wait(); p.err = p.command.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
			return
		default:
		}
		_ = p.command.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
		case <-time.After(8 * time.Second):
			_ = p.command.Process.Kill()
			<-p.done
			t.Error("gateway required forced termination")
		}
	})
	return p
}

func gatewayAddress(t *testing.T, p *gatewayProcess) string {
	t.Helper()
	select {
	case data := <-p.ready:
		var ready struct {
			State     string `json:"state"`
			Listeners []struct {
				Address string `json:"address"`
			} `json:"listeners"`
		}
		if err := json.Unmarshal(data, &ready); err != nil || ready.State != "ready" || len(ready.Listeners) != 1 {
			t.Fatal("invalid ready record", string(data), err)
		}
		return ready.Listeners[0].Address
	case <-p.done:
		t.Fatal("gateway exited before ready", p.err, p.stderr.String())
	case <-time.After(8 * time.Second):
		t.Fatal("gateway failed to become ready")
	}
	return ""
}
