//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestXPCClientStalledInputPreservesParentFlagsAndTerminates(t *testing.T) {
	_, client := buildXPCBundle(t, "org.airc.gateway.unused.stdin.probe")
	for _, interrupt := range []bool{true, false} {
		name := "deadline"
		if interrupt {
			name = "interrupt"
		}
		t.Run(name, func(t *testing.T) {
			input, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer writer.Close() // Remains open: the client must wait for data, not EOF.
			inputFD := input.Fd()
			flags, err := unix.FcntlInt(inputFD, unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			flags &^= unix.O_NONBLOCK
			if _, err := unix.FcntlInt(inputFD, unix.F_SETFL, flags); err != nil {
				t.Fatal(err)
			}
			checkFlags := func() {
				t.Helper()
				got, err := unix.FcntlInt(inputFD, unix.F_GETFL, 0)
				if err != nil || got != flags {
					t.Fatalf("client changed shared parent input flags: got=%#x want=%#x err=%v", got, flags, err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, client)
			command.Stdin = input
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = command.Wait(); close(done) }()
			defer func() {
				select {
				case <-done:
				default:
					_ = command.Process.Kill()
					<-done
				}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			var signal <-chan time.Time
			if interrupt {
				timer := time.NewTimer(300 * time.Millisecond)
				defer timer.Stop()
				signal = timer.C
			}
		loop:
			for {
				select {
				case <-done:
					break loop
				case <-ticker.C:
					checkFlags()
				case <-signal:
					if err := command.Process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
					signal = nil
				}
			}
			checkFlags()
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(waitErr, &exit) || stdout.Len() != 0 {
				t.Fatalf("stalled client termination: err=%v context=%v stdout=%q stderr=%q", waitErr, ctx.Err(), stdout.String(), stderr.String())
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if interrupt {
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
					t.Fatalf("client did not retain normal interrupt behavior: %v", waitErr)
				}
			} else if exit.ExitCode() != 124 && (exit.ExitCode() != 1 || stderr.String() != "timed out reading request from stdin\n") {
				t.Fatalf("client missed its own hard deadline: err=%v stderr=%q", waitErr, stderr.String())
			}
		})
	}
}
