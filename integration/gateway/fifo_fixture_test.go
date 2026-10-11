//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func configureFIFOFixture(t *testing.T, c config.Config) config.Config {
	t.Helper()
	root, err := filepath.EvalSymlinks(filepath.Dir(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	pair := filepath.Join(root, "fifo")
	if err := os.Mkdir(pair, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"input.fifo", "output.fifo"} {
		if err := unix.Mkfifo(filepath.Join(pair, name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.Listeners = []config.Listener{{ID: "fifo", Protocol: "fifo", Address: pair, Enabled: true, Operations: c.Listeners[0].Operations}}
	return c
}

func startFIFO(t *testing.T, binary string, c config.Config, signal syscall.Signal, expectFailure bool) (*fifoPeer, func(), *exec.Cmd) {
	t.Helper()
	pair := c.Listeners[0].Address
	reader, err := os.OpenFile(filepath.Join(pair, "output.fifo"), os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(pair), "fifo.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, binary, "serve-fifo", "--file", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	var stream *fifoPeer
	var once sync.Once
	stop := func() {
		once.Do(func() {
			defer cancel()
			if signal != 0 && !expectFailure {
				select {
				case <-done:
				default:
					if err := cmd.Process.Signal(signal); err != nil {
						t.Error(err)
					}
				}
			}
			if stream != nil {
				defer stream.Close()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Error("FIFO child did not join")
			}
			if expectFailure && waitErr == nil || !expectFailure && waitErr != nil {
				t.Errorf("FIFO exit: %v %s", waitErr, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Error("FIFO command polluted stdout", stdout.String())
			}
		})
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(3 * time.Second)
	var writer *os.File
	for {
		writer, err = os.OpenFile(filepath.Join(pair, "input.fifo"), os.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.ENXIO) {
			t.Fatal(err)
		}
		select {
		case <-done:
			t.Fatalf("FIFO child exited before peer connection: %v %s", waitErr, stderr.String())
		default:
		}
		if !time.Now().Before(deadline) {
			t.Fatal("FIFO input reader did not open")
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
	defer writer.Close()
	stream = newFIFOPeer(t, reader, writer)
	stream.childDone = done
	return stream, stop, cmd
}
