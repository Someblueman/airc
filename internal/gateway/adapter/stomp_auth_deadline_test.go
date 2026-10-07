//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestSTOMPPreauthenticationDeadlineCannotBeRenewed(t *testing.T) {
	for _, mode := range []string{"idle", "partial", "heartbeats"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			v, _ := amqpVerifierFixture(t)
			client, server := amqpTLSPair(t, tls.VersionTLS13)
			done := make(chan error, 1)
			started := time.Now()
			go func() {
				_, _, err := authenticateSTOMP(t.Context(), server, bufio.NewReader(server), v, []config.STOMPRoute{{Principal: "producer"}}, "fixture")
				done <- err
			}()
			if err := client.SetDeadline(started.Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := client.HandshakeContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			written := make(chan struct{})
			go func() {
				defer close(written)
				if mode == "partial" {
					_, _ = client.Write([]byte("CONNECT\nlogin:"))
					return
				}
				if mode == "idle" {
					return
				}
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				for range ticker.C {
					if _, err := client.Write([]byte{'\n'}); err != nil {
						return
					}
				}
			}()
			select {
			case err := <-done:
				if err == nil || time.Since(started) > 4*time.Second {
					t.Fatalf("deadline %s: %v", time.Since(started), err)
				}
			case <-time.After(4 * time.Second):
				closeMQTTConnection(client)
				closeMQTTConnection(server)
				<-done
				t.Fatal("preauthentication deadline extended")
			}
			select {
			case <-written:
			case <-time.After(time.Second):
				t.Fatal("preauthentication writer did not join")
			}
		})
	}
}
