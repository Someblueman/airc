//go:build linux

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type unixPacketConnection interface {
	net.Conn
	ReadMsgUnix([]byte, []byte) (int, int, int, *net.UnixAddr, error)
	WriteMsgUnix([]byte, []byte, *net.UnixAddr) (int, int, error)
}

// ListenUnixPacket creates a filesystem record listener with the same private
// path ownership and SO_PEERCRED gate as the Unix stream listener.
func ListenUnixPacket(path string) (net.Listener, error) {
	if len(path) > 107 {
		return nil, errors.New("unixpacket listener path exceeds 107 bytes")
	}
	return listenOwnedUnix("unixpacket", path)
}

// ServeUnixPacket owns and joins accepted connections and their request work.
// The caller bounds connections; requests share the global admission capacity.
// Requests use the encrypted detached profile or, on author listeners, the
// plain profile naming their author.
func ServeUnixPacket(ctx context.Context, listener net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	if listener == nil || d == nil || cap(requests) < 1 {
		return errors.New("invalid unixpacket listener, dispatcher or request capacity")
	}
	ctx, cancel := context.WithCancel(ctx)
	var connections sync.WaitGroup
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		connections.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		packet, ok := conn.(unixPacketConnection)
		if !ok {
			_ = conn.Close()
			return errors.New("unixpacket listener accepted a connection without record methods")
		}
		connections.Go(func() { _ = serveUnixPacketConnection(ctx, packet, d, requests) })
	}
}

func serveUnixPacketConnection(parent context.Context, conn unixPacketConnection, d *dispatch.Dispatcher, requests chan struct{}) error {
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		_ = conn.Close()
	}()
	var writes sync.Mutex
	first := true
	next := func() ([]byte, error) {
		body, err := readUnixPacketRecord(conn, first)
		first = false
		return body, err
	}
	write := func(response FrameResponse) error {
		writes.Lock()
		defer writes.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		err := writeUnixPacketRecord(conn, response)
		if err != nil && !errors.Is(err, store.ErrCapacity) {
			cancel()
		}
		return err
	}
	// Plain author requests carry their author; the owner-only socket and
	// SO_PEERCRED are the access gate, and AIRC sees listener:<id>.
	var plain *dispatch.Ingress
	if d.AuthorAccess() {
		plain = &dispatch.Ingress{Session: dispatch.NewSession()}
		defer plain.Session.Close()
	}
	return runCorrelated(ctx, correlatedTransport{next: next, write: write, watchModes: []string{"long_poll", "unixpacket_records"}, plain: plain}, d, requests, true)
}

func readUnixPacketRecord(conn unixPacketConnection, first bool) ([]byte, error) {
	idle := 30 * time.Second
	if first {
		idle = 5 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(idle)); err != nil {
		return nil, err
	}
	body := make([]byte, MaxRequestFrameBytes)
	// With no ancillary buffer, the kernel discards unreceived SCM_RIGHTS and
	// reports MSG_CTRUNC. Reject before parsing instead of installing descriptors.
	n, control, flags, _, err := conn.ReadMsgUnix(body, nil)
	if err != nil {
		return nil, err
	}
	if n < 1 || n > len(body) || control != 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 {
		return nil, store.ErrInvalid
	}
	return body[:n], nil
}

func writeUnixPacketRecord(conn unixPacketConnection, response FrameResponse) error {
	body, err := json.Marshal(response)
	if err != nil || len(body) > MaxResponseFrameBytes {
		return store.ErrCapacity
	}
	if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	n, control, err := conn.WriteMsgUnix(body, nil, nil)
	if err == nil && (n != len(body) || control != 0) {
		return io.ErrShortWrite
	}
	return err
}
