//go:build darwin && arm64

package gateway_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestQRToolAlarmInterruptsBlockedDiagnostics(t *testing.T) {
	tool := qrTool(t)
	pipeFDs := []int{0, 0}
	if err := unix.Pipe(pipeFDs); err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(pipeFDs[0]), "qr-stderr-reader")
	writer := os.NewFile(uintptr(pipeFDs[1]), "qr-stderr-writer")
	defer reader.Close()
	defer writer.Close()

	fd := writer.Fd()
	originalFlags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(fd, unix.F_SETFL, originalFlags|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	filled := 0
	sawFull := false
	chunk := make([]byte, 16*1024)
	for filled < 1<<20 {
		written, err := unix.Write(int(fd), chunk)
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			sawFull = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if written == 0 {
			t.Fatal("pipe fill made no progress")
		}
		filled += written
	}
	if !sawFull {
		t.Fatal("stderr pipe did not fill within the 1 MiB bound")
	}
	if _, err := unix.FcntlInt(fd, unix.F_SETFL, originalFlags); err != nil {
		t.Fatal(err)
	}
	flagsAfterFill, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil || flagsAfterFill&unix.O_NONBLOCK != 0 {
		t.Fatalf("stderr writer remains nonblocking before child launch: got=%#x nonblock=%#x err=%v", flagsAfterFill, unix.O_NONBLOCK, err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, tool, "invalid-command")
	command.Env = append(os.Environ(), "ASAN_OPTIONS=halt_on_error=1:abort_on_error=1")
	command.Stderr = writer
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = command.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = command.Process.Kill()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Errorf("blocked diagnostic process did not join after kill")
			}
		}
	})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(38 * time.Second):
		t.Fatalf("QR tool did not leave the blocked diagnostic before its 40-second outer deadline; elapsed=%v", time.Since(started))
	}
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(waitErr, &exit) {
		t.Fatalf("blocked diagnostic process exited unexpectedly: err=%v context=%v", waitErr, ctx.Err())
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	elapsed := time.Since(started)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGALRM || elapsed < 29*time.Second || elapsed >= 38*time.Second {
		t.Fatalf("QR tool did not terminate on its own 30-second alarm: status=%v elapsed=%v err=%v", exit.Sys(), elapsed, waitErr)
	}
}
