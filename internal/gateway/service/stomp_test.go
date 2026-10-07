//go:build darwin || linux

package service

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestSTOMPServiceDirectAdmissionAndJoinedShutdown(t *testing.T) {
	c := directoryServiceConfig(t)
	root := filepath.Dir(c.StateDir)
	c.Principals[0].BearerTokenFile = filepath.Join(root, "bearer")
	if err := auth.CreateCredential(c.Principals[0].BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	signingPath := filepath.Join(root, "response.pem")
	if err := os.WriteFile(signingPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	c.ResponseSigningKey = &config.ResponseSigningKey{ID: "response", PrivateKeyFile: signingPath}
	cert := testtls.New(t)
	c.Limits.Sessions, c.Limits.SessionsPerPrincipal = 1, 1
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 0, 0
	c.Limits.Workers = 1
	c.Listeners = []config.Listener{{ID: "stomp", Protocol: "stomp", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Send}, STOMP: &config.STOMP{Routes: []config.STOMPRoute{{Principal: c.Principals[0].ID, RequestDestination: "request", ResponseDestination: "response", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}}}
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
	// CONNECTED fences authentication and direct one-slot adapter admission.
	bearer, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := stompwire.Encode(stompwire.Frame{Command: "CONNECT", Headers: []stompwire.Header{{Key: "accept-version", Value: "1.2"}, {Key: "host", Value: c.Realm}, {Key: "login", Value: c.Principals[0].ID}, {Key: "passcode", Value: bearer}, {Key: "heart-beat", Value: "0,0"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(wire); err != nil {
		t.Fatal(err)
	}
	frame, err := stompwire.Read(bufio.NewReader(client))
	if err != nil || frame.Command != "CONNECTED" {
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
		t.Fatal("service did not join STOMP owners")
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
