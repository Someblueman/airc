package adapter

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapNet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coaptcp "github.com/plgd-dev/go-coap/v3/tcp/client"
)

func TestTCPObserveCancelAfterConsumedFrameKeepsCarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		conn := &tcpConsumedWriteGate{Conn: server, release: make(chan struct{})}
		peer := newTCPCancelPeer(t, conn)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m := tcpCancelNotification(ctx)
		done := make(chan error, 1)
		go func() { done <- peer.WriteMessage(m) }()
		if _, err := client.Read(make([]byte, coapTCPReceiveFrame)); err != nil {
			t.Fatal(err)
		}
		// The full frame is visible to the client, while its writer has not yet
		// returned. Observe cancellation must still leave its reply carrier open.
		synctest.Wait()
		cancel()
		synctest.Wait()
		closed := conn.closed.Load()
		close(conn.release)
		if err := <-done; err != nil || closed {
			t.Fatalf("consumed notification cancellation: err=%v carrier closed=%v", err, closed)
		}
		assertTCPCancelReply(t, peer, client)
	})
}

func TestTCPObserveCancelDoesNotInterruptConcurrentReply(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		conn := &tcpCancelDeadlineGate{
			tcpConsumedWriteGate: tcpConsumedWriteGate{Conn: server, release: make(chan struct{})},
			cancelStarted:        make(chan struct{}), cancelRelease: make(chan struct{}),
		}
		peer := newTCPCancelPeer(t, conn)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		notificationDone := make(chan error, 1)
		go func() { notificationDone <- peer.WriteMessage(tcpCancelNotification(ctx)) }()
		if _, err := client.Read(make([]byte, coapTCPReceiveFrame)); err != nil {
			t.Fatal(err)
		}
		cancel()
		<-conn.cancelStarted
		close(conn.release)
		// The physical write has returned, but cancellation has not settled.
		// Observe its ownership before starting another goroutine on the mutex;
		// mutex contention itself is not durably blocking in synctest.
		synctest.Wait()
		owned := !peer.carrier.writeMu.TryLock()
		if !owned {
			peer.carrier.writeMu.Unlock()
		}
		reply := pool.NewMessage(t.Context())
		reply.SetCode(codes.Content)
		reply.SetToken(message.Token("reply123"))
		replyDone := make(chan error, 1)
		go func() { replyDone <- peer.WriteMessage(reply) }()
		close(conn.cancelRelease)
		if err := <-notificationDone; err != nil {
			t.Fatal(err)
		}
		_, readErr := client.Read(make([]byte, coapTCPReceiveFrame))
		if err := <-replyDone; err != nil || readErr != nil {
			t.Fatalf("unrelated reply interrupted: write=%v read=%v", err, readErr)
		}
		if !owned {
			t.Fatal("completed notification released write ownership before cancellation settled")
		}
	})
}

func TestTCPObserveCancelDuringBlock2ClosesCarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		peer := newTCPCancelPeer(t, server)
		peer.carrier.peerBlockwise = true
		peer.carrier.peerMax = 64
		peer.blockwise = blockwise.New(peer.cc, 3*time.Second, func(error) {}, nil)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m := tcpCancelNotification(ctx)
		m.SetBody(bytes.NewReader(make([]byte, 512)))
		done := make(chan error, 1)
		go func() { done <- peer.WriteMessage(m) }()
		if _, err := client.Read(make([]byte, coapTCPReceiveFrame)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("partial notification cancellation: %v", err)
		}
		if len(peer.carrier.transfers) != 0 {
			t.Fatal("cancelled notification retained Block2 snapshot")
		}
		if _, err := client.Read(make([]byte, 1)); err == nil {
			t.Fatal("cancelled partial Block2 transfer left carrier open")
		}
	})
}

func TestTCPObserveCancelInterruptsBlockedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		started := make(chan struct{})
		peer := newTCPCancelPeer(t, &tcpWriteWitness{Conn: server, started: started})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- peer.WriteMessage(tcpCancelNotification(ctx)) }()
		<-started
		synctest.Wait()
		cancel()
		if err := <-done; err == nil {
			t.Fatal("cancelled blocked notification write succeeded")
		}
		if _, err := client.Read(make([]byte, 1)); err == nil {
			t.Fatal("cancelled blocked write left carrier open")
		}
	})
}

func TestTCPBlockTransferCancellationCompletionBoundary(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "incomplete", true: "completed"}[completed], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			transfer := &tcpBlockTransfer{done: make(chan struct{}), expires: time.Now().Add(time.Second)}
			if completed {
				close(transfer.done)
			}
			conn := &shortTCPConn{}
			// Model the pointer retained by a waiter when the final block closes
			// done, followed by cancellation before that waiter runs again.
			carrier := &coapTCPConn{Conn: conn, transfers: map[string]*tcpBlockTransfer{"12345678": transfer}}
			err := carrier.waitBlockTransfer(ctx, message.Token("12345678"))
			if completed {
				if err != nil || conn.closed.Load() {
					t.Fatalf("completed transfer lost cancellation race: err=%v closed=%v", err, conn.closed.Load())
				}
			} else if !errors.Is(err, context.Canceled) || !conn.closed.Load() {
				t.Fatalf("incomplete transfer cancellation: err=%v closed=%v", err, conn.closed.Load())
			}
		})
	}
}

func newTCPCancelPeer(t *testing.T, conn net.Conn) *coapTCPPeer {
	t.Helper()
	carrier := &coapTCPConn{Conn: conn, state: newCoAPState(make(chan struct{}, 4), "coaps_tcp_observe_agd1"), peerMax: coapTCPReceiveFrame}
	cfg := coaptcp.DefaultConfig
	cfg.DisableTCPSignalMessageCSM = true
	cfg.CloseSocket = true
	cc := coaptcp.NewConnWithOpts(coapNet.NewConn(carrier), &cfg)
	t.Cleanup(func() { _ = cc.Close(); _ = cc.Run() })
	return &coapTCPPeer{cc: cc, carrier: carrier}
}

func tcpCancelNotification(ctx context.Context) *pool.Message {
	m := pool.NewMessage(ctx)
	m.SetCode(codes.Content)
	m.SetToken(message.Token("12345678"))
	m.SetObserve(1)
	return m
}

func assertTCPCancelReply(t *testing.T, peer *coapTCPPeer, client net.Conn) {
	t.Helper()
	reply := pool.NewMessage(t.Context())
	reply.SetCode(codes.Content)
	reply.SetToken(message.Token("12345678"))
	done := make(chan error, 1)
	go func() { done <- peer.WriteMessage(reply) }()
	_, readErr := client.Read(make([]byte, coapTCPReceiveFrame))
	if err := <-done; err != nil || readErr != nil {
		t.Fatalf("cancellation reply: write=%v read=%v", err, readErr)
	}
}

// Hold the physical write's return after the peer has consumed the whole frame.
type tcpConsumedWriteGate struct {
	net.Conn
	release chan struct{}
	once    sync.Once
	closed  atomic.Bool
}

func (c *tcpConsumedWriteGate) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.once.Do(func() { <-c.release })
	return n, err
}

func (c *tcpConsumedWriteGate) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

type tcpCancelDeadlineGate struct {
	tcpConsumedWriteGate
	cancelStarted chan struct{}
	cancelRelease chan struct{}
}

func (c *tcpCancelDeadlineGate) SetWriteDeadline(deadline time.Time) error {
	if !deadline.After(time.Now()) {
		close(c.cancelStarted)
		<-c.cancelRelease
	}
	return c.Conn.SetWriteDeadline(deadline)
}
