//go:build linux

package adapter

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	canISOTPWorkerCommandFD = 3
	canISOTPWorkerReceiveFD = 4
	canISOTPWorkerErrorMax  = 240
	canISOTPWorkerAckWait   = time.Second
	canISOTPWorkerWriteWait = 3 * time.Second
)

// RunCANISOTPWorker serves the parent process over inherited Unix packet
// sockets. The child handles only opaque ISO-TP payloads; request admission and
// response authentication remain in the parent.
func RunCANISOTPWorker(address string) error {
	command, err := inheritedCANISOTPWorkerSocket(canISOTPWorkerCommandFD, "can-isotp-command")
	if err != nil {
		return err
	}
	receive, err := inheritedCANISOTPWorkerSocket(canISOTPWorkerReceiveFD, "can-isotp-receive")
	if err != nil {
		_ = writeCANISOTPWorkerStatus(command, err)
		_ = command.Close()
		return err
	}
	failStartup := func(startErr error) error {
		_ = writeCANISOTPWorkerStatus(command, startErr)
		_ = command.Close()
		_ = receive.Close()
		return startErr
	}

	local, err := ParseCANISOTPAddr(address)
	if err != nil {
		return failStartup(err)
	}
	native, err := listenCANISOTPNative(local)
	if err != nil {
		return failStartup(err)
	}
	if err := writeCANISOTPWorkerStatus(command, nil); err != nil {
		_ = command.Close()
		_ = receive.Close()
		_ = native.Close()
		return err
	}
	return serveCANISOTPWorker(command, receive, native, local)
}

func inheritedCANISOTPWorkerSocket(fd int, name string) (*net.UnixConn, error) {
	socketType, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil {
		return nil, fmt.Errorf("inspect CAN ISO-TP worker socket type: %w", err)
	}
	domain, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_DOMAIN)
	if err != nil {
		return nil, fmt.Errorf("inspect CAN ISO-TP worker socket domain: %w", err)
	}
	if socketType != unix.SOCK_SEQPACKET || domain != unix.AF_UNIX {
		return nil, errors.New("invalid CAN ISO-TP worker IPC socket")
	}
	peer, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return nil, fmt.Errorf("inspect CAN ISO-TP worker peer credentials: %w", err)
	}
	if peer.Pid != int32(os.Getppid()) || peer.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("CAN ISO-TP worker IPC peer is not its parent")
	}

	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		return nil, errors.New("invalid inherited CAN ISO-TP worker descriptor")
	}
	conn, err := net.FileConn(file)
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("wrap CAN ISO-TP worker socket: %w", errors.Join(err, closeErr))
	}
	if closeErr != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("close inherited CAN ISO-TP worker descriptor: %w", closeErr)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok || unixConn.LocalAddr().Network() != "unixpacket" {
		_ = conn.Close()
		return nil, errors.New("inherited CAN ISO-TP worker descriptor is not a Unix packet socket")
	}
	return unixConn, nil
}

func writeCANISOTPWorkerStatus(conn *net.UnixConn, err error) error {
	status := []byte{0}
	if err != nil {
		message := []byte(err.Error())
		if len(message) > canISOTPWorkerErrorMax {
			message = message[:canISOTPWorkerErrorMax]
		}
		status = append([]byte{1}, message...)
	}
	if setErr := conn.SetWriteDeadline(time.Now().Add(canISOTPWorkerAckWait)); setErr != nil {
		return setErr
	}
	n, oobn, writeErr := conn.WriteMsgUnix(status, nil, nil)
	if writeErr != nil {
		return writeErr
	}
	if n != len(status) || oobn != 0 {
		return errors.New("short CAN ISO-TP worker status packet")
	}
	return nil
}

func serveCANISOTPWorker(command, receive *net.UnixConn, native *canISOTPNativePackets, local *CANISOTPAddr) error {
	peer := &CANISOTPAddr{Interface: local.Interface, RXID: local.TXID, TXID: local.RXID}
	errCh := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		errCh <- canISOTPWorkerCommandLoop(command, native, peer)
	}()
	go func() {
		defer workers.Done()
		errCh <- canISOTPWorkerReceiveLoop(receive, native, peer)
	}()

	firstErr := <-errCh
	_ = command.Close()
	_ = receive.Close()
	closeErr := native.Close()
	workers.Wait()
	secondErr := <-errCh
	return errors.Join(firstErr, secondErr, closeErr)
}

func canISOTPWorkerCommandLoop(command *net.UnixConn, native *canISOTPNativePackets, peer *CANISOTPAddr) error {
	var packet [canISOTPMaxPDU + 1]byte
	for {
		n, oobn, flags, _, err := command.ReadMsgUnix(packet[:], nil)
		if err != nil {
			return err
		}
		if n < 1 || n > canISOTPMaxPDU || oobn != 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			protocolErr := errors.New("invalid CAN ISO-TP worker command packet")
			_ = writeCANISOTPWorkerStatus(command, protocolErr)
			return protocolErr
		}
		if err := native.SetWriteDeadline(time.Now().Add(canISOTPWorkerWriteWait)); err != nil {
			return err
		}
		written, writeErr := native.WriteTo(packet[:n], peer)
		if writeErr == nil && written != n {
			writeErr = errors.New("short CAN ISO-TP worker write")
		}
		if err := writeCANISOTPWorkerStatus(command, writeErr); err != nil {
			return err
		}
	}
}

func canISOTPWorkerReceiveLoop(receive *net.UnixConn, native *canISOTPNativePackets, peer *CANISOTPAddr) error {
	var packet [canISOTPMaxPDU + 1]byte
	for {
		n, source, err := native.ReadFrom(packet[:])
		if err != nil {
			return err
		}
		gotPeer, ok := source.(*CANISOTPAddr)
		if !ok || gotPeer.Interface != peer.Interface || gotPeer.RXID != peer.RXID || gotPeer.TXID != peer.TXID || n < 1 || n > canISOTPMaxPDU {
			return errors.New("invalid CAN ISO-TP worker receive packet")
		}
		if err := receive.SetWriteDeadline(time.Now().Add(canISOTPWorkerAckWait)); err != nil {
			return err
		}
		written, oobn, err := receive.WriteMsgUnix(packet[:n], nil, nil)
		if err != nil {
			return err
		}
		if written != n || oobn != 0 {
			return errors.New("short CAN ISO-TP worker receive packet")
		}
	}
}
