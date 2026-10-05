package adapter

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapNet "github.com/plgd-dev/go-coap/v3/net"
	coaptcp "github.com/plgd-dev/go-coap/v3/tcp/client"
	"github.com/plgd-dev/go-coap/v3/tcp/coder"
)

func TestServeCoAPTCPRejectsInvalidInputs(t *testing.T) {
	if err := ServeCoAPTCP(context.Background(), nil, nil, make(chan struct{}, 1)); err == nil {
		t.Fatal("nil TCP peer accepted")
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	if err := ServeCoAPTCP(context.Background(), server, nil, make(chan struct{}, 1)); err == nil {
		t.Fatal("nil dispatcher accepted")
	}
}

func tcpWire(t *testing.T, m *pool.Message) []byte {
	t.Helper()
	b, err := m.MarshalWithEncoder(coder.DefaultCoder)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTCPWriteGateAndActivityRequireConsumedContent(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	token := message.Token("12345678")
	state := &coapState{active: map[string]*coapWatch{string(token): {ready: make(chan struct{}), mid: -1}}, authenticated: map[string]bool{string(token): true}}
	started := make(chan struct{})
	carrier := &coapTCPConn{Conn: &tcpWriteWitness{Conn: server, started: started}, state: state, peerMax: coapTCPReceiveFrame}
	carrier.authenticated.Store(1)
	content := pool.NewMessage(t.Context())
	content.SetToken(token)
	content.SetCode(codes.Content)
	content.SetObserve(0)
	done := make(chan error, 1)
	go func() { _, err := carrier.Write(tcpWire(t, content)); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("physical write did not start")
	}
	select {
	case <-state.active[string(token)].ready:
		t.Fatal("ready opened before peer consumed frame")
	default:
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1200)
	if _, err := client.Read(buf); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-state.active[string(token)].ready:
	default:
		t.Fatal("ready did not open after successful Content")
	}
	contentAuth := carrier.authenticated.Load()
	if contentAuth <= 1 {
		t.Fatal("successful Content did not renew authenticated activity")
	}
	partial := pool.NewMessage(t.Context())
	partial.SetToken(token)
	partial.SetCode(codes.Continue)
	done = make(chan error, 1)
	go func() { _, err := carrier.Write(tcpWire(t, partial)); done <- err }()
	if _, err := client.Read(buf); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if carrier.authenticated.Load() != contentAuth {
		t.Fatal("Continue renewed authenticated activity")
	}
}

func TestTCPReceiveCSMValidation(t *testing.T) {
	carrier := &coapTCPConn{peerMax: coapTCPReceiveFrame}
	valid := pool.NewMessage(context.Background())
	valid.SetCode(codes.CSM)
	valid.SetOptionUint32(message.TCPMaxMessageSize, 900)
	valid.SetOptionBytes(message.TCPBlockWiseTransfer, nil)
	if err := carrier.receiveCSM(valid); err != nil || !carrier.hasCSM() || carrier.peerMax != 900 {
		t.Fatalf("valid CSM err=%v max=%d", err, carrier.peerMax)
	}
	bad := pool.NewMessage(context.Background())
	bad.SetMessage(message.Message{Code: codes.CSM, Options: message.Options{{ID: message.TCPMaxMessageSize, Value: []byte{1, 2, 3, 4, 5}}}})
	if err := carrier.receiveCSM(bad); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad length err=%v", err)
	}
	dup := pool.NewMessage(context.Background())
	dup.SetMessage(message.Message{Code: codes.CSM, Options: message.Options{{ID: message.TCPMaxMessageSize, Value: []byte{4}}, {ID: message.TCPMaxMessageSize, Value: []byte{5}}}})
	if err := carrier.receiveCSM(dup); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("duplicate err=%v", err)
	}
	critical := pool.NewMessage(context.Background())
	critical.SetMessage(message.Message{Code: codes.CSM, Options: message.Options{{ID: 7}}})
	if err := carrier.receiveCSM(critical); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("critical unknown err=%v", err)
	}
}

type shortTCPConn struct{ closed atomic.Bool }

func (c *shortTCPConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *shortTCPConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 1, io.ErrShortWrite
}
func (c *shortTCPConn) Close() error                     { c.closed.Store(true); return nil }
func (c *shortTCPConn) LocalAddr() net.Addr              { return shortAddr("local") }
func (c *shortTCPConn) RemoteAddr() net.Addr             { return shortAddr("remote") }
func (c *shortTCPConn) SetDeadline(time.Time) error      { return nil }
func (c *shortTCPConn) SetReadDeadline(time.Time) error  { return nil }
func (c *shortTCPConn) SetWriteDeadline(time.Time) error { return nil }

type shortAddr string

func (a shortAddr) Network() string { return "test" }
func (a shortAddr) String() string  { return string(a) }

func TestTCPPartialWriteClosesWithoutReady(t *testing.T) {
	token := message.Token("12345678")
	state := &coapState{active: map[string]*coapWatch{string(token): {ready: make(chan struct{})}}, authenticated: map[string]bool{string(token): true}}
	conn := &shortTCPConn{}
	carrier := &coapTCPConn{Conn: conn, state: state, peerMax: coapTCPReceiveFrame}
	m := pool.NewMessage(context.Background())
	m.SetToken(token)
	m.SetCode(codes.Content)
	m.SetObserve(0)
	if _, err := carrier.Write(tcpWire(t, m)); err == nil {
		t.Fatal("partial write unexpectedly succeeded")
	}
	if !conn.closed.Load() {
		t.Fatal("partial write did not close carrier")
	}
	select {
	case <-state.active[string(token)].ready:
		t.Fatal("failed write opened ready gate")
	default:
	}
}

func TestServeCoAPTCPClosesAfterMissingCSM(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		done <- ServeCoAPTCP(context.Background(), server, &dispatch.Dispatcher{}, make(chan struct{}, 4))
	}()
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1200)
	if n, err := client.Read(buf); err != nil || n == 0 {
		t.Fatalf("server CSM n=%d err=%v", n, err)
	}
	select {
	case <-time.After(6500 * time.Millisecond):
		t.Fatal("missing CSM did not close within bound")
	case <-done:
	}
}

func TestTCPWriteContentionClosesCarrier(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	state := newCoAPState(make(chan struct{}, 4), "coaps_tcp_observe_agd1")
	started := make(chan struct{})
	carrier := &coapTCPConn{Conn: &tcpWriteWitness{Conn: server, started: started}, state: state, peerMax: coapTCPReceiveFrame}
	cfg := coaptcp.DefaultConfig
	cfg.DisableTCPSignalMessageCSM = true
	cfg.CloseSocket = true
	cc := coaptcp.NewConnWithOpts(coapNet.NewConn(carrier), &cfg)
	peer := &coapTCPPeer{cc: cc, carrier: carrier}
	first := pool.NewMessage(context.Background())
	first.SetCode(codes.Content)
	firstDone := make(chan error, 1)
	go func() { firstDone <- peer.WriteMessage(first) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first writer did not acquire physical carrier")
	}
	secondCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	second := pool.NewMessage(secondCtx)
	second.SetCode(codes.Content)
	startedAt := time.Now()
	if err := peer.WriteMessage(second); err == nil {
		t.Fatal("contended writer unexpectedly succeeded")
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("contended cancellation took %s, beyond its own deadline", elapsed)
	}
	select {
	case <-time.After(time.Second):
		t.Fatal("first writer not released after carrier close")
	case <-firstDone:
	}
	_ = cc.Close()
	_ = cc.Run()
}

// Witness the physical write entry before testing a blocked writer or gate.
type tcpWriteWitness struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *tcpWriteWitness) Write(b []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(b)
}

func TestServeCoAPTCPInitialWriteFailureReleasesPrivateReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &shortTCPConn{}
		err := ServeCoAPTCP(t.Context(), conn, &dispatch.Dispatcher{}, make(chan struct{}, 1))
		if !errors.Is(err, io.ErrShortWrite) || !conn.closed.Load() {
			t.Fatalf("initial CSM failure=%v closed=%v", err, conn.closed.Load())
		}
	})
}
