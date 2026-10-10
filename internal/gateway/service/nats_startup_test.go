//go:build darwin || linux

package service

import (
	"bytes"
	"net"
	"os"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestNATSStartupFailureDoesNotClaimReadyOrRetainOwnership(t *testing.T) {
	for _, kind := range []string{"response_signer", "later_bind"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, _ := natsServiceConfig(t)
			first, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := first.Addr().String()
			_ = first.Close()
			c.Listeners[0].Address = address
			if kind == "response_signer" {
				if err := os.WriteFile(c.ResponseSigningKey.PrivateKeyFile, []byte("invalid private key"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				occupied, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer occupied.Close()
				c.Listeners = append(c.Listeners, config.Listener{ID: "occupied", Protocol: "http", Enabled: true, Address: occupied.Addr().String(), Operations: []core.Operation{core.Send}})
			}
			var output bytes.Buffer
			if err := Run(t.Context(), c, &output); err == nil || output.Len() != 0 {
				t.Fatal("failed startup claimed ready", err)
			}
			rebound, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatal("failed startup retained socket", err)
			}
			_ = rebound.Close()
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("failed startup retained journal", err)
			}
			_ = journal.Close()
		})
	}
}
