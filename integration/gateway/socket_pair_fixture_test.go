//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/sys/unix"
)

func startSocketPair(t *testing.T, binary string, c config.Config, signal syscall.Signal, expectFailure bool) (net.Conn, func()) {
	t.Helper()
	c.Listeners[0].ID, c.Listeners[0].Protocol = "pair", "socketpair"
	path := filepath.Join(filepath.Dir(c.StateDir), "socketpair.json")
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	child := os.NewFile(uintptr(fds[0]), "child-pair")
	defer child.Close()
	parent := os.NewFile(uintptr(fds[1]), "parent-pair")
	defer parent.Close()
	conn, err := net.FileConn(parent)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "serve-socketpair", "--file", path)
	cmd.ExtraFiles = []*os.File{child}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Logf("owned socket-pair gateway pid=%d", cmd.Process.Pid)
	child.Close()
	parent.Close()
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			defer conn.Close()
			select {
			case <-done:
			default:
				if !expectFailure {
					_ = cmd.Process.Signal(signal)
				}
			}
			select {
			case <-done:
				if (waitErr != nil) != expectFailure {
					t.Errorf("socket-pair shutdown: %v stderr=%s", waitErr, stderr.String())
				}
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Errorf("socket-pair required forced termination: %s", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("socket-pair stdout pollution: %q", stdout.String())
			}
		})
	}
	t.Cleanup(stop)
	return conn, stop
}
