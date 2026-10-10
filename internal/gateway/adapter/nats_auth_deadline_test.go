//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSAuthenticationBlockedINFO(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		v, _ := amqpVerifierFixture(t)
		server, peer := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = peer.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		if cancelled {
			cancel()
		}
		info := natsInfo{ServerID: "fixture", ServerName: "fixture", Host: "127.0.0.1", MaxPayload: natswire.MaxPayload, Proto: 1, AuthRequired: true, TLSRequired: true}
		security := &tls.Config{}
		started := time.Now()
		_, err := authenticateNATS(ctx, server, security, v, []config.NATSRoute{{Principal: "producer"}}, info)
		cancel()
		if err == nil || time.Since(started) > natsDeadline+time.Second {
			t.Fatalf("blocked INFO deadline: %v", err)
		}
		if security.MinVersion != 0 {
			t.Fatal("mutated caller TLS configuration")
		}
	}
}

func TestNATSAuthenticationCancellationAndTLSVersion(t *testing.T) {
	for _, phase := range []string{"before-tls", "connect", "old-tls", "cleartext"} {
		t.Run(phase, func(t *testing.T) {
			version := uint16(tls.VersionTLS13)
			if phase == "old-tls" {
				version = tls.VersionTLS12
			}
			client, cancel, done, token := natsAuthFixture(t, version)
			written := make(chan struct{})
			if phase != "cleartext" {
				close(written)
			}
			switch phase {
			case "connect":
				if err := client.HandshakeContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				cancel()
			case "before-tls":
				cancel()
			case "old-tls":
				if err := client.HandshakeContext(t.Context()); err == nil {
					t.Fatal("TLS 1.2 accepted")
				}
			case "cleartext":
				go func() { defer close(written); _, _ = client.NetConn().Write(natsConnectBytes(token)) }()
			}
			var r natsAuthResult
			if phase == "connect" || phase == "before-tls" {
				select {
				case r = <-done:
				case <-time.After(time.Second):
					t.Fatal("cancellation did not join promptly")
				}
			} else {
				r = natsAuthFinished(t, done)
			}
			if r.err == nil {
				t.Fatal("unsafe authentication")
			}
			if phase == "connect" || phase == "before-tls" {
				var b [1]byte
				if _, err := io.ReadFull(client.NetConn(), b[:]); err == nil {
					t.Fatal("cancelled socket open")
				}
			}
			closeMQTTConnection(client)
			<-written
		})
	}
}

func TestNATSAuthenticationAbsoluteDeadline(t *testing.T) {
	for _, phase := range []string{"idle-tls", "idle-connect", "trickle-connect", "preconnect-ping"} {
		t.Run(phase, func(t *testing.T) {
			client, _, done, _ := natsAuthFixture(t, tls.VersionTLS13)
			started := time.Now()
			if phase != "idle-tls" {
				if err := client.HandshakeContext(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			finished := make(chan struct{})
			if phase == "trickle-connect" {
				go func() {
					defer close(finished)
					for _, b := range []byte("CONNECT {\"auth_token\":\"") {
						if _, err := client.Write([]byte{b}); err != nil {
							return
						}
						time.Sleep(250 * time.Millisecond)
					}
				}()
			} else {
				close(finished)
			}
			if phase == "preconnect-ping" {
				if _, err := client.Write([]byte("PING\r\n")); err != nil {
					t.Fatal(err)
				}
			}
			r := natsAuthFinished(t, done)
			if r.err == nil {
				t.Fatal("unauthenticated peer accepted")
			}
			if elapsed := time.Since(started); elapsed > natsDeadline+time.Second {
				t.Fatalf("authentication deadline renewed: %v", elapsed)
			}
			closeMQTTConnection(client)
			<-finished
		})
	}
}
