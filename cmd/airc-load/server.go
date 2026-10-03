package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/server"
)

type resource struct {
	At              time.Time `json:"at"`
	HeapBytes       uint64    `json:"heap_bytes"`
	TotalAllocBytes uint64    `json:"total_alloc_bytes"`
	Goroutines      int       `json:"goroutines"`
	MaxRSSBytes     int64     `json:"max_rss_bytes"`
	CPUSeconds      float64   `json:"cpu_seconds"`
	GC              uint32    `json:"gc_cycles"`
}

func resources() resource {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	rss := usage.Maxrss
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	cpu := float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
	return resource{time.Now().UTC(), m.HeapAlloc, m.TotalAlloc, runtime.NumGoroutine(), rss, cpu, m.NumGC}
}

type childMessage struct {
	Address  string    `json:"address,omitempty"`
	Resource *resource `json:"resource,omitempty"`
}

func serveChild(ctx context.Context, c config, agents int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	s := server.New(server.Config{MaxConnections: agents, HistoryLimit: 10000, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if c.Persist {
		if err := s.RestoreHistory(filepath.Join(c.Out, "history.jsonl")); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(listener) }()
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(childMessage{Address: listener.Addr().String()}); err != nil {
		_ = listener.Close()
		return err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case runErr = <-done:
			break loop
		case <-ticker.C:
			r := resources()
			if err := encoder.Encode(childMessage{Resource: &r}); err != nil {
				runErr = err
				break loop
			}
		}
	}
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	runErr = errors.Join(runErr, s.Shutdown(shutdown))
	r := resources()
	return errors.Join(runErr, encoder.Encode(childMessage{Resource: &r}))
}

type child struct {
	address    string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	done       chan error
	samples    []resource // owned by decoder until done closes
	stopCancel func() bool
}

func startChild(ctx context.Context, c config, agents int, dir string) (*child, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "-serve", "-agents", fmt.Sprint(agents), "-out", dir, "-persist="+fmt.Sprint(c.Persist))
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	p := &child{cmd: cmd, stdin: in, done: make(chan error, 1)}
	p.stopCancel = context.AfterFunc(ctx, func() { _ = in.Close() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		var decodeErr error
		for scanner.Scan() {
			var message childMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				decodeErr = err
				break
			}
			if message.Address != "" {
				select {
				case ready <- message.Address:
				default:
				}
			}
			if message.Resource != nil {
				p.samples = append(p.samples, *message.Resource)
			}
		}
		if decodeErr != nil {
			_ = in.Close()
		}
		p.done <- errors.Join(decodeErr, scanner.Err(), cmd.Wait())
	}()
	select {
	case p.address = <-ready:
		return p, nil
	case err := <-p.done:
		p.stopCancel()
		_ = in.Close()
		return nil, fmt.Errorf("server exited before readiness: %v", err)
	case <-ctx.Done():
		_ = p.close()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		_ = p.close()
		return nil, errors.New("server readiness timeout")
	}
}

func (p *child) close() error {
	p.stopCancel()
	_ = p.stdin.Close()
	select {
	case err := <-p.done:
		return err
	case <-time.After(7 * time.Second):
		_ = p.cmd.Process.Kill()
		return errors.Join(errors.New("server shutdown timed out"), <-p.done)
	}
}
