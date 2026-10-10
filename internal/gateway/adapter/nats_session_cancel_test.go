//go:build darwin || linux

package adapter

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSSessionShutdownCancelsActiveNativeRead(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	native, err := net.Listen("unix", f.c.Bindings[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := native.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = native.Close()
		<-finished
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})
	sealer, _, _, _ := natsResultFixture(t)
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	_, dial, token, cancel, done := natsDispatchListenerFixture(t, slots, 1, f, sealer, requests)
	client := dial()
	r, connected := natsClientConnect(t, client, token)
	if connected.Command != "+OK" {
		t.Fatal("CONNECT")
	}
	if _, err := client.Write([]byte("SUB airc.response 1\r\n")); err != nil {
		t.Fatal(err)
	}
	if reply, err := natswire.Read(r); err != nil || reply.Command != "+OK" {
		t.Fatal("SUB", err)
	}
	request := core.Request{Version: 1, RequestID: "active-read", Operation: core.HistoryRead, Target: amqpRequest().Target, Limit: 1}
	wire, err := natswire.Encode(natsAdmissionFrame(t, f, request, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(wire); err != nil {
		t.Fatal(err)
	}
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("native read not started")
	}
	defer conn.Close()
	if len(requests) != 1 {
		t.Fatal("active read did not own capacity")
	}
	started := time.Now()
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second || len(slots) != 0 || len(requests) != 0 {
		t.Fatal("shutdown did not join dispatch")
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatal("native read socket retained", err)
	}
}
