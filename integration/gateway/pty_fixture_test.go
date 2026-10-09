//go:build darwin || linux

package gateway_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"golang.org/x/sys/unix"
)

type ptyBroker struct {
	path     string
	masterFD int
	slaveFD  int
	master   *os.File
	slave    *os.File
	original unix.Termios
}

func newPTYBroker(t *testing.T) *ptyBroker {
	t.Helper()
	masterFD, path, err := openPTY()
	if err != nil {
		t.Fatal("create PTY master", err)
	}
	master := os.NewFile(uintptr(masterFD), "pty-master")
	if err := master.SetDeadline(time.Now().Add(time.Second)); err != nil {
		_ = master.Close()
		t.Fatal("PTY master does not support deadlines", err)
	}
	if err := master.SetDeadline(time.Time{}); err != nil {
		_ = master.Close()
		t.Fatal("clear PTY master deadline", err)
	}
	slaveFD, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		_ = master.Close()
		t.Fatal("open PTY slave", err)
	}
	slave := os.NewFile(uintptr(slaveFD), "pty-slave")
	original, err := preparePTY(slaveFD)
	if err != nil {
		_ = slave.Close()
		_ = master.Close()
		t.Fatal("read initial PTY settings", err)
	}
	b := &ptyBroker{path: path, master: master, slave: slave, masterFD: masterFD, slaveFD: slaveFD, original: original}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("PTY cleanup: %v", err)
		}
	})
	return b
}

func (b *ptyBroker) waitReady(t *testing.T, process *uartProcess) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		settings := b.inspect(t)
		ready := true
		for _, key := range []string{"baud", "eight_bits", "one_stop_bit", "no_parity", "no_flow_control", "raw", "exclusive_lock"} {
			v, ok := settings[key]
			ready = ready && ok && v
		}
		if ready {
			return
		}
		select {
		case <-ticker.C:
		case <-process.done:
			t.Fatalf("UART exited before claiming PTY: %v; settings=%v; stderr=%s", process.err, settings, process.stderr.String())
		case <-deadline.C:
			t.Fatalf("UART did not configure and exclusively claim PTY; settings=%v", settings)
		}
	}
}

func (b *ptyBroker) waitWriteBlocked(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		state := b.query(t, "blocked")
		if state["readable"] && !state["writable"] {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("UART response never filled the unread PTY output queue")
		}
	}
}

func (b *ptyBroker) waitOutputDrained(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		state := b.query(t, "blocked")
		if !state["readable"] {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("UART short response left unread PTY output")
		}
	}
}

func (b *ptyBroker) inspect(t *testing.T) map[string]bool {
	t.Helper()
	return b.query(t, "inspect")
}

func (b *ptyBroker) query(t *testing.T, command string) map[string]bool {
	t.Helper()
	var state map[string]bool
	switch command {
	case "inspect":
		term, err := readPTYTermios(b.slaveFD)
		if err != nil {
			t.Fatal("read PTY settings", err)
		}
		locked, err := ptyLockContended(b.slaveFD)
		if err != nil {
			t.Fatal("test PTY flock", err)
		}
		state = map[string]bool{
			"baud":            ptyBaud115200(term),
			"eight_bits":      uint64(term.Cflag)&uint64(unix.CSIZE) == uint64(unix.CS8),
			"one_stop_bit":    uint64(term.Cflag)&uint64(unix.CSTOPB) == 0,
			"no_parity":       uint64(term.Cflag)&uint64(unix.PARENB) == 0,
			"no_flow_control": uint64(term.Cflag)&uint64(unix.CRTSCTS) == 0 && uint64(term.Iflag)&uint64(unix.IXON|unix.IXOFF|unix.IXANY) == 0,
			"raw":             uint64(term.Iflag)&uint64(unix.IGNBRK|unix.BRKINT|unix.PARMRK|unix.ISTRIP|unix.INLCR|unix.IGNCR|unix.ICRNL|unix.INPCK) == 0 && uint64(term.Oflag)&uint64(unix.OPOST) == 0 && uint64(term.Lflag)&uint64(unix.ICANON|unix.ECHO|unix.ECHONL|unix.ISIG|unix.IEXTEN) == 0,
			"exclusive_lock":  locked,
		}
	case "restored":
		term, err := readPTYTermios(b.slaveFD)
		if err != nil {
			t.Fatal("read restored PTY settings", err)
		}
		locked, err := ptyLockContended(b.slaveFD)
		if err != nil {
			t.Fatal("check restored PTY flock", err)
		}
		state = map[string]bool{"restored": term == b.original, "lock_free": !locked}
	case "blocked":
		var fds [2]unix.PollFd
		fds[0] = unix.PollFd{Fd: int32(b.masterFD), Events: unix.POLLIN}
		fds[1] = unix.PollFd{Fd: int32(b.slaveFD), Events: unix.POLLOUT}
		if _, err := unix.Poll(fds[:], 0); err != nil {
			t.Fatal("poll PTY readiness", err)
		}
		for _, fd := range fds {
			if fd.Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
				t.Fatalf("PTY poll failed: revents=%x", fd.Revents)
			}
		}
		state = map[string]bool{"readable": fds[0].Revents&unix.POLLIN != 0, "writable": fds[1].Revents&unix.POLLOUT != 0}
	default:
		t.Fatalf("unknown PTY query %q", command)
	}
	return state
}

func ptyLockContended(fd int) (bool, error) {
	err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, unix.Flock(fd, unix.LOCK_UN)
}

func (b *ptyBroker) Close() error {
	var errs []error
	if b.master != nil {
		if err := b.master.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
		b.master = nil
	}
	if b.slave != nil {
		errs = append(errs, b.slave.Close())
		b.slave = nil
	}
	return errors.Join(errs...)
}

type ptyConn struct{ file *os.File }

func (c *ptyConn) Read(p []byte) (int, error)         { return c.file.Read(p) }
func (c *ptyConn) Write(p []byte) (int, error)        { return c.file.Write(p) }
func (c *ptyConn) Close() error                       { return c.file.Close() }
func (c *ptyConn) SetDeadline(t time.Time) error      { return c.file.SetDeadline(t) }
func (c *ptyConn) SetReadDeadline(t time.Time) error  { return c.file.SetReadDeadline(t) }
func (c *ptyConn) SetWriteDeadline(t time.Time) error { return c.file.SetWriteDeadline(t) }
func (c *ptyConn) LocalAddr() net.Addr                { return ptyAddr("pty-master") }
func (c *ptyConn) RemoteAddr() net.Addr               { return ptyAddr("pty-slave") }

type ptyAddr string

func (a ptyAddr) Network() string { return "pty" }
func (a ptyAddr) String() string  { return string(a) }

var _ net.Conn = (*ptyConn)(nil)

func writeSplitUARTFrame(t *testing.T, conn net.Conn, frame adapter.FrameRequest) {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	parts := [][]byte{header[:1], header[1:3], header[3:], body[:1], body[1 : len(body)/2], body[len(body)/2:]}
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		n, err := conn.Write(part)
		if err != nil {
			t.Fatal(err)
		}
		if n != len(part) {
			t.Fatal(io.ErrShortWrite)
		}
	}
}
