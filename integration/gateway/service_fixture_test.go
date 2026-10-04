//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

type gatewayProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
	stderr  bytes.Buffer
	ready   chan []byte
}

func gatewayBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "airc-gateway")
	cmd := exec.Command("go", "build", "-race", "-o", path, "./cmd/airc-gateway")
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build isolated gateway: %v %s", err, output)
	}
	return path
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
	p.command.Env = append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+root, "XDG_STATE_HOME="+root)
	p.command.Stderr = &p.stderr
	stdout, err := p.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Logf("owned gateway pid=%d", p.command.Process.Pid)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			p.ready <- append([]byte(nil), scanner.Bytes()...)
		}
		for scanner.Scan() {
		}
	}()
	go func() { <-readDone; p.err = p.command.Wait(); close(p.done) }()
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
