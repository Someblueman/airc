//go:build darwin || linux

package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestAMQPServiceDirectAdmissionAndJoinedShutdown(t *testing.T) {
	c := upstreamServiceConfig(t)
	cert := testtls.New(t)
	c.Limits.Sessions, c.Limits.SessionsPerAuthor = 1, 1
	c.Limits.Watches, c.Limits.WatchesPerAuthor = 0, 0
	c.Limits.Workers = 1
	c.Listeners = []config.Listener{{ID: "amqp", Protocol: "amqp", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Send}, AMQP: &config.AMQP{Routes: []config.AMQPRoute{{RequestAddress: "request", ResponseAddress: "response", Operations: []core.Operation{core.Send}}}}}}
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
	joined := false
	t.Cleanup(func() {
		cancel()
		_ = reader.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("service cleanup did not join")
			}
		}
	})
	var ready struct{ Listeners []struct{ Address string } }
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 1 {
		t.Fatal(ready, err)
	}
	address := ready.Listeners[0].Address
	client, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, cert.Client.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer client.NetConn().Close()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// A one-slot service must admit this connection once, then speak SASL rather
	// than framed JSON. The mechanism reply fences actual adapter ownership.
	header := []byte{'A', 'M', 'Q', 'P', 3, 1, 0, 0}
	if _, err := client.Write(header); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 8)
	if _, err := io.ReadFull(client, reply); err != nil || string(reply) != string(header) {
		t.Fatal(reply, err)
	}
	frame, err := amqpwire.ReadFrame(client, 512)
	if err != nil || frame.Type != 1 {
		t.Fatal(frame, err)
	}
	rejected, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, cert.Client.Clone())
	if err == nil {
		_ = rejected.NetConn().Close()
		t.Fatal("second socket bypassed the shared slot")
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not join incomplete SASL")
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("service retained listener", err)
	}
	_ = rebound.Close()
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("service retained journal", err)
	}
	_ = journal.Close()
}
