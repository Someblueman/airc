//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestInheritedPipeStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPipeProcess$")
	cmd.Env = append(os.Environ(), "AIRC_PIPE_TEST=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte("pipe payload")); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	got, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v: %s", err, stderr.String())
	}
	if string(got) != "pipe payload" {
		t.Fatalf("stdout polluted: %q", got)
	}
}

func TestPipeProcess(t *testing.T) {
	if os.Getenv("AIRC_PIPE_TEST") != "1" {
		return
	}
	p, err := OpenPipeStream(os.Stdin, os.Stdout)
	if err != nil {
		os.Exit(2)
	}
	if p.SetReadDeadline(time.Now().Add(time.Second)) != nil || p.SetWriteDeadline(time.Now().Add(time.Second)) != nil {
		os.Exit(3)
	}
	if _, err := io.Copy(p, p); err != nil {
		os.Exit(4)
	}
	if err := p.Close(); err != nil {
		os.Exit(5)
	}
	// Closing owned duplicates must preserve inherited descriptor ownership.
	if _, err := os.Stdin.Stat(); err != nil {
		os.Exit(6)
	}
	if _, err := os.Stdout.Stat(); err != nil {
		os.Exit(7)
	}
	os.Exit(0)
}
