//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPTLSAuthenticationPreservesPipelinedBytes(t *testing.T) {
	for _, command := range []string{"CONNECT", "STOMP"} {
		t.Run(command, func(t *testing.T) {
			v, token := amqpVerifierFixture(t)
			client, server := amqpTLSPair(t, tls.VersionTLS13)
			reader := bufio.NewReader(server)
			done := make(chan error, 1)
			go func() {
				identity, _, err := authenticateSTOMP(t.Context(), server, reader, v, []config.STOMPRoute{{Principal: "producer"}}, "fixture")
				if err == nil && identity.ID() != "producer" {
					err = auth.ErrAuthentication
				}
				done <- err
			}()
			if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := client.HandshakeContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			wire, err := stompwire.Encode(stompConnect(command, token))
			if err != nil {
				t.Fatal(err)
			}
			wire = append([]byte("\r\n"), wire...)
			wire = append(wire, []byte("DISCONNECT\n\n\x00")...)
			written := make(chan error, 1)
			go func() { _, err := client.Write(wire); written <- err }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("authentication did not join")
			}
			f, err := stompwire.Read(reader)
			if err != nil || f.Command != "DISCONNECT" {
				t.Fatalf("pipelined %+v %v", f, err)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSTOMPAuthenticationCancellationAndTLSVersion(t *testing.T) {
	v, _ := amqpVerifierFixture(t)
	for _, version := range []uint16{tls.VersionTLS13, tls.VersionTLS12} {
		t.Run(map[uint16]string{tls.VersionTLS13: "cancel", tls.VersionTLS12: "old-tls"}[version], func(t *testing.T) {
			client, server := amqpTLSPair(t, version)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := authenticateSTOMP(ctx, server, bufio.NewReader(server), v, []config.STOMPRoute{{Principal: "producer"}}, "fixture")
				done <- err
			}()
			if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := client.HandshakeContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if version == tls.VersionTLS13 {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe authentication")
				}
			case <-time.After(time.Second):
				t.Fatal("authentication did not join")
			}
			if version == tls.VersionTLS13 {
				var b [1]byte
				if _, err := io.ReadFull(client, b[:]); err == nil {
					t.Fatal("cancelled connection open")
				}
			}
		})
	}
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	if _, _, err := authenticateSTOMP(t.Context(), server, bufio.NewReader(server), v, []config.STOMPRoute{{Principal: "producer"}}, "fixture"); err == nil {
		t.Fatal("cleartext authenticated")
	}
}
