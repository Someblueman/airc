//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func buildDBusClientProcessTest(t *testing.T) string {
	t.Helper()
	skipIfDBusGLibUnavailable(t)
	output := filepath.Join(t.TempDir(), "dbus-bin")
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "build-dbus-bridge.sh"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, script, "--output", output, "--sanitize")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build native D-Bus bridge: %v: %s", err, data)
	}
	client := filepath.Join(output, "dbus-client")
	if info, err := os.Stat(client); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("D-Bus client missing or not executable: %s stat=%v info=%v", client, err, info)
	}
	return client
}

func runDBusClientInput(t *testing.T, client, socket string, input []byte) (string, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, client, "--socket", socket)
	command.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return stderr.String(), append([]byte(nil), stdout.Bytes()...), err
}

func TestDBusClientStalledInputPreservesParentFlagsAndTerminates(t *testing.T) {
	client := buildDBusClientProcessTest(t)
	socket := filepath.Join("/tmp", fmt.Sprintf("airc-dbus-client-no-peer-%d.sock", os.Getpid()))
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
			defer writer.Close() // Keep stdin open so the client must wait for EOF or data.
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
					t.Fatalf("client changed shared parent stdin flags: got=%#x want=%#x err=%v", got, flags, err)
				}
			}

			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, client, "--socket", socket)
			command.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
			command.Stdin = input
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
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

			ticker := time.NewTicker(20 * time.Millisecond)
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
					t.Fatalf("client did not retain ordinary interrupt behavior: %v", waitErr)
				}
			} else {
				elapsed := time.Since(started)
				timedOut := exit.ExitCode() == 124 ||
					(exit.ExitCode() == 1 && stderr.String() == "timed out reading request from stdin\n")
				if !timedOut || elapsed < 7*time.Second || elapsed > 10*time.Second {
					t.Fatalf("client missed its 8-second hard deadline: err=%v elapsed=%v stderr=%q", waitErr, elapsed, stderr.String())
				}
			}
		})
	}
}

func TestDBusClientRejectsEmptyAndOversizedInputBeforeConnecting(t *testing.T) {
	client := buildDBusClientProcessTest(t)
	socket := filepath.Join("/tmp", fmt.Sprintf("airc-dbus-client-no-peer-%d.sock", os.Getpid()))
	for _, tc := range []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "empty", input: nil, want: "request stdin is empty"},
		{name: "oversized", input: bytes.Repeat([]byte{'x'}, (16<<10)+1), want: "request exceeds 16384 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, stdout, err := runDBusClientInput(t, client, socket, tc.input)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() == 0 || len(stdout) != 0 || !strings.Contains(stderr, tc.want) || strings.Contains(stderr, "peer connection failed") {
				t.Fatalf("input refusal did not happen before peer connection: stdout=%d stderr=%q err=%v", len(stdout), stderr, err)
			}
		})
	}
}

func TestDBusBuildRefusesExistingOutput(t *testing.T) {
	skipIfDBusGLibUnavailable(t)
	output := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(output, "sentinel")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "build-dbus-bridge.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(script, "--output", output)
	command.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
	data, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(data), "output already exists") {
		t.Fatalf("builder did not refuse existing output: err=%v output=%s", err, data)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("existing output was modified: data=%q err=%v", got, err)
	}
}
