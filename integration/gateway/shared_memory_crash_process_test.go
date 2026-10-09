//go:build (darwin || linux) && arm64

package gateway_test

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

type sharedMemoryCrashChild struct {
	command  *exec.Cmd
	stderr   bytes.Buffer
	waitDone chan struct{}
	waitErr  error
	stage    chan sharedMemoryCrashStage
	readDone chan struct{}
}

type sharedMemoryCrashStage struct {
	value string
	err   error
}

func startSharedMemoryCrashChild(t *testing.T, name, directory, mode string) *sharedMemoryCrashChild {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestSharedMemoryCrashChild$", "-test.timeout=15s")
	command.Env = sharedMemoryCrashEnvironment(name, directory, mode)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child := &sharedMemoryCrashChild{command: command, waitDone: make(chan struct{}), stage: make(chan sharedMemoryCrashStage, 1), readDone: make(chan struct{})}
	command.Stderr = &child.stderr
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		t.Fatal(err)
	}
	go func() {
		defer close(child.readDone)
		line, err := bufio.NewReaderSize(stdout, 128).ReadSlice('\n')
		child.stage <- sharedMemoryCrashStage{value: strings.TrimSpace(string(line)), err: err}
	}()
	go func() {
		child.waitErr = command.Wait()
		close(child.waitDone)
	}()
	t.Cleanup(func() { child.cleanup(t) })
	return child
}

func (c *sharedMemoryCrashChild) waitStage(t *testing.T) string {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case got := <-c.stage:
		if got.err != nil {
			t.Fatalf("child stage witness was invalid: %v", got.err)
		}
		return got.value
	case <-c.waitDone:
		t.Fatalf("child exited before stage witness: %v; stderr=%s", c.waitError(), c.stderr.String())
	case <-timer.C:
		t.Fatal("timed out waiting for child stage witness")
	}
	return ""
}

func (c *sharedMemoryCrashChild) killAndWait(t *testing.T) {
	t.Helper()
	if err := c.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("SIGKILL child: %v", err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-c.waitDone:
		err := c.waitError()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("child did not exit from SIGKILL: %v", err)
		}
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatalf("child exit was not SIGKILL: %v", err)
		}
	case <-timer.C:
		t.Fatal("timed out reaping SIGKILL child")
	}
}

func (c *sharedMemoryCrashChild) cleanup(t *testing.T) {
	t.Helper()
	select {
	case <-c.waitDone:
	default:
		if err := c.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("cleanup kill shared-memory child: %v", err)
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-c.waitDone:
			if err := c.waitError(); err == nil {
				t.Error("shared-memory child exited successfully instead of being killed")
			}
		case <-timer.C:
			t.Error("timed out reaping shared-memory child during cleanup")
		}
	}
	select {
	case <-c.readDone:
	case <-time.After(5 * time.Second):
		t.Error("timed out joining shared-memory child output reader")
	}
}

func (c *sharedMemoryCrashChild) waitError() error {
	return c.waitErr
}
