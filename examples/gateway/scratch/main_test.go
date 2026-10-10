package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestStopJoinsAlreadyExitedChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := &child{cmd: exec.CommandContext(ctx, os.Args[0], "-test.run=^$"), done: make(chan struct{})}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	// Reaping can precede completion of the pipe-reader's bookkeeping. Signal
	// now returns ErrProcessDone, but stop must still await that publication.
	go func() { time.Sleep(20 * time.Millisecond); close(p.done) }()
	if err := p.stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	default:
		t.Fatal("already-exited child cleanup returned before joining")
	}
}
