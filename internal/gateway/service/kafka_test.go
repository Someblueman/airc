//go:build darwin || linux

package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaServiceReadyAndJoinedShutdown(t *testing.T) {
	for _, phase := range []string{"authentication", "fetch"} {
		t.Run(phase, func(t *testing.T) {
			c, cert := kafkaServiceConfig(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			c.Kafka[0].Brokers[0].Address = listener.Addr().String()
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
				if phase == "authentication" {
					api, v, _, _, err := kafkaServiceRequest(t, conn)
					if err != nil || api != 18 || v != 0 {
						t.Error("version negotiation", err)
						return
					}
				} else {
					if !kafkaServiceAuth(t, conn) {
						return
					}
					api, v, corr, _, err := kafkaServiceRequest(t, conn)
					if err != nil || api != 3 || v != 8 {
						t.Error("metadata", err)
						return
					}
					if kafkaServiceReply(conn, corr, kafkaServiceMetadataWire(c.Kafka[0], 7, 7, 8)) != nil {
						return
					}
					api, v, _, body, err := kafkaServiceRequest(t, conn)
					want, _ := kafkawire.Fetch("events", 2, 8, 23)
					if err != nil || api != 1 || v != 11 || !bytes.Equal(body, want) {
						t.Error("fixed saved-position fetch", err)
						return
					}
				}
				close(reached)
				var b [1]byte
				if _, err := conn.Read(b[:]); err == nil {
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
			if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 0 || len(ready.Brokers) != 1 || ready.Brokers[0].ID != "kafka" || ready.Brokers[0].Protocol != "kafka" {
				t.Fatal("ready", err)
			}
			select {
			case <-reached:
			case <-time.After(3 * time.Second):
				t.Fatal("owner phase missing")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("owner not joined")
			}
			select {
			case <-peerDone:
			case <-time.After(time.Second):
				t.Fatal("socket retained")
			}
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("journal retained", err)
			}
			defer journal.Close()
			pos, err := journal.ProvisionKafka(t.Context(), "kafka")
			if err != nil || pos != (store.KafkaPosition{NextOffset: 23, LeaderEpoch: -1}) {
				t.Fatal("cancel advanced position", pos, err)
			}
		})
	}
}
