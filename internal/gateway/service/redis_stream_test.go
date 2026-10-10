//go:build darwin || linux

package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamsServiceReadyAndJoinedShutdown(t *testing.T) {
	for _, phase := range []string{"authentication", "pending_read"} {
		t.Run(phase, func(t *testing.T) {
			c, cert, _, _ := natsServiceConfig(t)
			c.Listeners = []config.Listener{}
			c.Limits.Sessions, c.Limits.SessionsPerPrincipal, c.Limits.Workers = 1, 1, 1
			c.Limits.Watches, c.Limits.WatchesPerPrincipal = 0, 0
			c.Principals[0].BearerTokenFile = ""
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			password := filepath.Join(filepath.Dir(c.StateDir), "stream-password")
			if err := os.WriteFile(password, []byte("password"), 0600); err != nil {
				t.Fatal(err)
			}
			c.RedisStreams = []config.RedisStreams{{ID: "streams", Enabled: true, Namespace: "broker", Address: listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath,
				Credential: config.RedisCredential{Username: "reader", PasswordFile: password}, Group: "gateway", Consumer: "owner",
				Route: config.RedisStreamRoute{Principal: c.Principals[0].ID, Stream: "events", ResultStream: "results", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reached, peerDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(peerDone)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				conn := tls.Server(raw, cert.Server)
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				v, err := rediswire.Read(r)
				if err != nil || len(v.Array) != 3 || string(v.Array[0].Bytes) != "AUTH" || string(v.Array[1].Bytes) != "reader" || string(v.Array[2].Bytes) != "password" {
					t.Error("AUTH", err)
					return
				}
				if phase == "pending_read" {
					if _, err := conn.Write([]byte("+OK\r\n")); err != nil {
						t.Error(err)
						return
					}
					v, err = rediswire.Read(r)
					expected := []string{"XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "STREAMS", "events", "0-0"}
					if err != nil || len(v.Array) != len(expected) {
						t.Error("pending read", err)
						return
					}
					for i, value := range expected {
						if string(v.Array[i].Bytes) != value {
							t.Error("pending command")
							return
						}
					}
				}
				close(reached)
				if _, err := r.ReadByte(); err == nil {
					t.Error("unexpected command after cancellation")
				}
			}()
			reader, writer := io.Pipe()
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() { defer close(finished); err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
			t.Cleanup(func() { cancel(); _ = listener.Close(); _ = reader.Close(); <-finished; <-peerDone })
			var ready struct {
				Listeners []json.RawMessage
				Brokers   []struct{ ID, Protocol string }
			}
			if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 0 || len(ready.Brokers) != 1 || ready.Brokers[0].ID != "streams" || ready.Brokers[0].Protocol != "redis/streams" {
				t.Fatal("ready", err)
			}
			select {
			case <-reached:
			case <-time.After(3 * time.Second):
				t.Fatal("owner did not reach phase")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("service did not join blocked owner")
			}
			select {
			case <-peerDone:
			case <-time.After(time.Second):
				t.Fatal("socket retained after service joined")
			}
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("journal retained", err)
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
