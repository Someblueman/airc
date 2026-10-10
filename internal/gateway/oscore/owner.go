//go:build darwin || linux

package oscore

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type Options struct {
	Binary     string
	Address    string
	Realm      string
	Store      *store.Store
	Material   *auth.OSCOREContext
	Dispatcher *dispatch.Dispatcher
	Requests   chan struct{}
}

var ErrNativeStopped = errors.New("native OSCORE worker stopped unexpectedly")

// Owner retains both private sockets and the exclusive context lease until the
// child, persistence worker, RPC worker, and cancellation hook have joined.
type Owner struct {
	address *net.UDPAddr
	process *os.Process
	cancel  context.CancelFunc
	closing atomic.Bool
	once    sync.Once
	done    chan struct{}
	err     error
}

func (o *Owner) Addr() net.Addr {
	return &net.UDPAddr{IP: append(net.IP(nil), o.address.IP...), Port: o.address.Port}
}
func (o *Owner) Wait() error { <-o.done; return o.err }
func (o *Owner) Close() error {
	o.once.Do(func() { o.closing.Store(true); o.cancel() })
	return o.Wait()
}

// Start launches the owner-selected native executable without a shell. Only
// the literal listen IP and port enter argv; keys use inherited private fd 3,
// application RPC uses fd 4. Standard input/output/error are discarded. No key
// provisioning occurs. One canonical context can have only one live owner.
func Start(parent context.Context, options Options) (*Owner, error) {
	if options.Store == nil || options.Material == nil || options.Dispatcher == nil || cap(options.Requests) < 1 || cap(options.Requests) > 256 || options.Material.Owner().Realm != options.Realm {
		return nil, store.ErrInvalid
	}
	host, portText, err := net.SplitHostPort(options.Address)
	port, portErr := strconv.Atoi(portText)
	if err != nil || net.ParseIP(host) == nil || portErr != nil || port < 0 || port > 65535 {
		return nil, store.ErrInvalid
	}
	binaryPath, err := ownedExecutable(options.Binary)
	if err != nil {
		return nil, err
	}
	digest, err := options.Material.Digest()
	if err != nil {
		return nil, err
	}
	owner := options.Material.Owner()
	if _, err := options.Store.LoadOSCORE(parent, store.OSCOREKey{Digest: digest, Principal: owner.Principal, Binding: owner.Binding}); err != nil {
		return nil, err
	}
	release, err := claimContext(digest)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			release()
		}
	}()
	persistence, childPersistence, err := privateSocketPair()
	if err != nil {
		return nil, err
	}
	defer childPersistence.Close()
	rpc, childRPC, err := privateSocketPair()
	if err != nil {
		_ = persistence.Close()
		return nil, err
	}
	defer childRPC.Close()
	ctx, cancel := context.WithCancel(parent)
	ioStopped := make(chan struct{})
	stopIO := context.AfterFunc(ctx, func() {
		_ = persistence.Close()
		_ = rpc.Close()
		close(ioStopped)
	})
	command := exec.CommandContext(ctx, binaryPath, host, strconv.Itoa(port))
	command.Env = []string{"LC_ALL=C"}
	command.ExtraFiles = []*os.File{childPersistence, childRPC}
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 3 * time.Second
	if err := command.Start(); err != nil {
		cancel()
		if !stopIO() {
			<-ioStopped
		}
		_ = persistence.Close()
		_ = rpc.Close()
		return nil, err
	}
	_ = childPersistence.Close()
	_ = childRPC.Close()
	waitDone := make(chan struct{})
	var waitErr error
	go func() { waitErr = command.Wait(); close(waitDone) }()
	persistDone := make(chan error, 1)
	go func() {
		persistDone <- ServePersistence(ctx, persistence, options.Store, options.Material, options.Realm)
	}()
	var ready [6]byte
	err = rpc.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err == nil {
		_, err = io.ReadFull(rpc, ready[:])
	}
	actualPort := int(binary.BigEndian.Uint16(ready[4:]))
	if err == nil && (string(ready[:4]) != "AGO1" || actualPort == 0 || (port != 0 && actualPort != port)) {
		err = ErrProtocol
	}
	if err != nil {
		cancel()
		<-waitDone
		persistenceErr := <-persistDone
		if !stopIO() {
			<-ioStopped
		}
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		return nil, errors.Join(err, meaningfulWorkerError(persistenceErr), waitErr)
	}
	rpcDone := make(chan error, 1)
	go func() { rpcDone <- adapter.ServeOSCORERPC(ctx, rpc, options.Dispatcher, options.Requests, owner) }()
	o := &Owner{address: &net.UDPAddr{IP: net.ParseIP(host), Port: actualPort}, process: command.Process, cancel: cancel, done: make(chan struct{})}
	started = true
	go func() {
		var cause, persistenceErr, rpcErr error
		persistenceJoined, rpcJoined := false, false
		select {
		case <-ctx.Done():
		case <-waitDone:
			cause = errors.Join(ErrNativeStopped, waitErr)
		case persistenceErr = <-persistDone:
			persistenceJoined = true
			cause = errors.Join(ErrNativeStopped, persistenceErr)
		case rpcErr = <-rpcDone:
			rpcJoined = true
			cause = errors.Join(ErrNativeStopped, rpcErr)
		}
		requested := o.closing.Load() || parent.Err() != nil
		cancel()
		<-waitDone
		if !persistenceJoined {
			persistenceErr = <-persistDone
		}
		if !rpcJoined {
			rpcErr = <-rpcDone
		}
		if !stopIO() {
			<-ioStopped
		}
		if !requested {
			o.err = errors.Join(cause, meaningfulWorkerError(persistenceErr), meaningfulWorkerError(rpcErr))
		}
		release()
		close(o.done)
	}()
	return o, nil
}

func meaningfulWorkerError(err error) error {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
