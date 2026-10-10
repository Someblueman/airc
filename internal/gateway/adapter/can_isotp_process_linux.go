//go:build linux

package adapter

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// CANISOTPPackets supervises the process that owns the ISO-TP socket. Linux
// ISO-TP close can otherwise wait indefinitely for peer flow-control WAITs.
// No native credentials or response-signing keys enter the worker.
type CANISOTPPackets struct {
	command          *net.UnixConn
	results          *net.UnixConn
	local            CANISOTPAddr
	child            *exec.Cmd
	done             chan struct{}
	stopped          chan struct{}
	writeGate        chan struct{}
	mu               sync.Mutex
	writeDeadline    time.Time
	activeWriteLimit time.Time
	closeOnce        sync.Once
	closeErr         error
}

func ListenCANISOTP(address *CANISOTPAddr) (*CANISOTPPackets, error) {
	if !validCANISOTPAddr(address) {
		return nil, errors.New("invalid CAN ISO-TP address")
	}
	command, childCommand, err := canWorkerPair()
	if err != nil {
		return nil, err
	}
	defer childCommand.Close()
	results, childResults, err := canWorkerPair()
	if err != nil {
		command.Close()
		return nil, err
	}
	defer childResults.Close()
	c := &CANISOTPPackets{command: command, results: results, local: *address, done: make(chan struct{}), stopped: make(chan struct{}), writeGate: make(chan struct{}, 1)}
	// /proc/self/exe executes this exact binary, including after an on-disk
	// replacement. The internal command accepts only inherited private IPC.
	c.child = exec.Command("/proc/self/exe", "__can-isotp-worker", address.String())
	c.child.Env = []string{}
	c.child.ExtraFiles = []*os.File{childCommand, childResults}
	c.child.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := c.child.Start(); err != nil {
		command.Close()
		results.Close()
		return nil, fmt.Errorf("start CAN worker: %w", err)
	}
	// Close the parent's copies immediately: otherwise EOF cannot identify
	// worker death while startup awaits its bounded ready acknowledgement.
	childCommand.Close()
	childResults.Close()
	go func() { _ = c.child.Wait(); close(c.done) }()
	if err := command.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		c.Close()
		return nil, err
	}
	if err := canWorkerAck(command); err != nil {
		c.Close()
		return nil, fmt.Errorf("prepare CAN worker: %w", err)
	}
	if err := command.SetReadDeadline(time.Time{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func canWorkerPair() (*net.UnixConn, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	for _, fd := range fds {
		if err := setCANISOtpsocketBuffers(fd); err != nil {
			_ = unix.Close(fds[0])
			_ = unix.Close(fds[1])
			return nil, nil, err
		}
	}
	parent := os.NewFile(uintptr(fds[0]), "can-worker-parent")
	child := os.NewFile(uintptr(fds[1]), "can-worker-child")
	conn, err := net.FileConn(parent)
	parent.Close()
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	packet, ok := conn.(*net.UnixConn)
	if !ok || packet.LocalAddr().Network() != "unixpacket" {
		conn.Close()
		child.Close()
		return nil, nil, errors.New("invalid CAN worker IPC")
	}
	return packet, child, nil
}

func canWorkerAck(conn *net.UnixConn) error {
	var ack [canISOTPWorkerErrorMax + 2]byte
	n, _, flags, _, err := conn.ReadMsgUnix(ack[:], nil)
	if err != nil {
		return err
	}
	if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n < 1 || n > canISOTPWorkerErrorMax+1 {
		return errors.New("invalid CAN worker acknowledgement")
	}
	if ack[0] == 0 && n == 1 {
		return nil
	}
	if ack[0] == 1 && n > 1 {
		return fmt.Errorf("CAN worker: %s", ack[1:n])
	}
	return errors.New("invalid CAN worker acknowledgement")
}

func (c *CANISOTPPackets) ReadFrom(body []byte) (int, net.Addr, error) {
	var packet [canISOTPMaxPDU + 1]byte
	n, _, flags, _, err := c.results.ReadMsgUnix(packet[:], nil)
	if err != nil {
		return 0, nil, err
	}
	if n == 0 {
		return 0, nil, io.EOF
	}
	if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n > canISOTPMaxPDU || len(body) < n {
		return 0, nil, errors.New("invalid or oversized CAN worker PDU")
	}
	copy(body, packet[:n])
	return n, &CANISOTPAddr{Interface: c.local.Interface, RXID: c.local.TXID, TXID: c.local.RXID}, nil
}

func (c *CANISOTPPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	peer, ok := destination.(*CANISOTPAddr)
	if !ok || !validCANISOTPAddr(peer) || peer.Interface != c.local.Interface || peer.RXID != c.local.TXID || peer.TXID != c.local.RXID || len(body) == 0 || len(body) > canISOTPMaxPDU {
		return 0, errors.New("invalid CAN worker destination or PDU")
	}
	limit := time.Now().Add(3 * time.Second)
	c.mu.Lock()
	deadline := canEarlierDeadline(c.writeDeadline, limit)
	c.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case c.writeGate <- struct{}{}:
	case <-c.stopped:
		return 0, net.ErrClosed
	case <-timer.C:
		return 0, os.ErrDeadlineExceeded
	}
	defer func() { <-c.writeGate }()
	c.mu.Lock()
	c.activeWriteLimit = limit
	deadline = canEarlierDeadline(c.writeDeadline, limit)
	err := c.command.SetDeadline(deadline)
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.activeWriteLimit = time.Time{}; c.mu.Unlock() }()
	if err != nil {
		return 0, err
	}
	if !time.Now().Before(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.command.Write(body)
	if err != nil {
		_ = c.Close()
		return 0, err
	}
	if n != len(body) {
		_ = c.Close()
		return 0, io.ErrShortWrite
	}
	if err := canWorkerAck(c.command); err != nil {
		// A timed-out acknowledgement cannot be reused for the next write.
		// End this association; durable intent remains owned by the gateway.
		_ = c.Close()
		return 0, err
	}
	return n, nil
}

func canEarlierDeadline(deadline, limit time.Time) time.Time {
	if deadline.IsZero() || deadline.After(limit) {
		return limit
	}
	return deadline
}

func (c *CANISOTPPackets) LocalAddr() net.Addr               { copy := c.local; return &copy }
func (c *CANISOTPPackets) SetReadDeadline(t time.Time) error { return c.results.SetReadDeadline(t) }
func (c *CANISOTPPackets) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = t
	if !c.activeWriteLimit.IsZero() {
		t = canEarlierDeadline(t, c.activeWriteLimit)
	}
	return c.command.SetDeadline(t)
}
func (c *CANISOTPPackets) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}

func (c *CANISOTPPackets) Close() error {
	c.closeOnce.Do(func() {
		close(c.stopped)
		c.closeErr = errors.Join(c.command.Close(), c.results.Close())
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-c.done:
		case <-timer.C:
			// SIGKILL interrupts the kernel's interruptible ISO-TP release
			// wait. Always reap the child before returning to the service.
			if err := c.child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				c.closeErr = errors.Join(c.closeErr, err)
			}
			<-c.done
		}
	})
	return c.closeErr
}

var _ net.PacketConn = (*CANISOTPPackets)(nil)
