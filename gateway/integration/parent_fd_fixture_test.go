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

	"github.com/Someblueman/airc/gateway/internal/config"
	"golang.org/x/sys/unix"
)

func startParentFD(t *testing.T, binary string, c config.Config, protocol string, signal syscall.Signal, expectFailure bool) (net.Conn, func(), *exec.Cmd) {
	t.Helper()
	c.Listeners[0].ID, c.Listeners[0].Protocol = "parent-fd", protocol
	path := filepath.Join(filepath.Dir(c.StateDir), protocol+".json")
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	var child, parent *os.File
	switch protocol {
	case "socketpair":
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		child = os.NewFile(uintptr(fds[0]), "child-pair")
		parent = os.NewFile(uintptr(fds[1]), "parent-pair")
	case "inherited":
		child, parent = inheritedNamedUnixFiles(t)
	default:
		t.Fatal("unsupported parent FD fixture protocol", protocol)
	}
	defer child.Close()
	defer parent.Close()
	conn, err := net.FileConn(parent)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "serve-"+protocol, "--file", path)
	cmd.ExtraFiles = []*os.File{child}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Logf("owned parent-FD gateway pid=%d", cmd.Process.Pid)
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
				if !expectFailure && signal != 0 {
					_ = cmd.Process.Signal(signal)
				}
			}
			select {
			case <-done:
				if (waitErr != nil) != expectFailure {
					t.Errorf("parent-FD shutdown: %v stderr=%s", waitErr, stderr.String())
				}
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Errorf("parent-FD required forced termination: %s", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("parent-FD stdout pollution: %q", stdout.String())
			}
		})
	}
	t.Cleanup(stop)
	return conn, stop, cmd
}
