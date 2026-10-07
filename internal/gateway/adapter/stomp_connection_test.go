//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func stompConnectionFixture(t *testing.T, heartbeat string) (*tls.Conn, *stompConnection, *bufio.Reader) {
	t.Helper()
	v, token := amqpVerifierFixture(t)
	client, server := amqpTLSPair(t, tls.VersionTLS13)
	ctx, cancel := context.WithCancel(t.Context())
	type result struct {
		c   *stompConnection
		err error
	}
	done := make(chan result, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		c, err := openSTOMPConnection(ctx, server, v, []config.STOMPRoute{{Principal: "producer"}}, "fixture")
		done <- result{c, err}
	}()
	t.Cleanup(func() { cancel(); closeMQTTConnection(client); closeMQTTConnection(server); <-finished })
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	f := stompConnect("CONNECT", token)
	f.Headers[4].Value = heartbeat
	wire, err := stompwire.Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(wire); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(client)
	connected, err := stompwire.Read(r)
	version, _ := connected.Header("version")
	offered, _ := connected.Header("heart-beat")
	if err != nil || connected.Command != "CONNECTED" || version != "1.2" || offered != "1000,1000" {
		t.Fatalf("CONNECTED %+v %v", connected, err)
	}
	var got result
	select {
	case got = <-done:
	case <-time.After(time.Second):
		t.Fatal("open owner did not join")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	t.Cleanup(got.c.close)
	return client, got.c, r
}

func TestSTOMPConnectionHeartbeatAndTerminalOrdering(t *testing.T) {
	client, c, r := stompConnectionFixture(t, "0,1000")
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	f, err := stompwire.Read(r)
	if err != nil || f.Command != "" {
		t.Fatalf("heartbeat %+v %v", f, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- c.writeBefore(stompwire.Frame{Command: "RECEIPT", Headers: []stompwire.Header{{Key: "receipt-id", Value: "bye"}}}, time.Time{}, true)
	}()
	if f, err = stompwire.Read(r); err != nil || f.Command != "RECEIPT" {
		t.Fatalf("terminal %+v %v", f, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := c.write(stompwire.Frame{Command: "MESSAGE"}); !errors.Is(err, stompwire.ErrFrame) {
		t.Fatal("frame after terminal", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(1300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := stompwire.Read(r); err == nil {
		t.Fatal("heartbeat after terminal")
	}
	c.close()
}

func TestSTOMPConnectionCancellationJoinsBlockedHeartbeat(t *testing.T) {
	client, c, _ := stompConnectionFixture(t, "0,1000")
	// The TLS heartbeat writer blocks on net.Pipe because the peer never reads.
	time.Sleep(1100 * time.Millisecond)
	done := make(chan struct{})
	go func() { c.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked heartbeat did not join")
	}
	var b [1]byte
	if _, err := client.Read(b[:]); err == nil {
		t.Fatal("closed socket readable")
	}
	if c.ctx.Err() == nil {
		t.Fatal("connection context live")
	}
}

func TestSTOMPConnectionDisabledHeartbeatAndApplicationFrame(t *testing.T) {
	client, c, r := stompConnectionFixture(t, "0,0")
	select {
	case <-c.heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("disabled heartbeat worker live")
	}
	written := make(chan error, 1)
	go func() { _, err := client.Write([]byte("SEND\ncontent-length:3\n\na\x00b\x00")); written <- err }()
	f, err := c.readBefore(time.Now().Add(time.Second))
	if err != nil || f.Command != "SEND" || len(f.Body) != 3 || f.Body[1] != 0 {
		t.Fatalf("frame %+v %v", f, err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := stompwire.Read(r); err == nil {
		t.Fatal("disabled heartbeat sent")
	}
}
