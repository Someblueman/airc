//go:build darwin && arm64

package service

import (
	"bytes"
	"errors"
	"net"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestICMPv6StartupFailureDoesNotClaimReadyOrRetainJournal(t *testing.T) {
	for _, kind := range []string{"missing_interface", "later_bind"} {
		t.Run(kind, func(t *testing.T) {
			c := upstreamServiceConfig(t)
			c.Listeners = []config.Listener{{
				ID: "icmp", Protocol: "icmp_v6", Address: "[::1]:61001", Enabled: true,
				Operations: []core.Operation{core.Send}, ICMPv6: &config.ICMPv6{Interface: "lo0"},
			}}
			if kind == "missing_interface" {
				c.Listeners[0].ICMPv6.Interface = "agw-no-such-interface"
			} else {
				occupied, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer occupied.Close()
				c.Listeners = append(c.Listeners, config.Listener{
					ID: "later", Protocol: "http", Enabled: true, Address: occupied.Addr().String(),
					Operations: []core.Operation{core.Send},
				})
			}
			if err := c.Validate(); err != nil {
				t.Fatal("fixture must reach runtime ownership", err)
			}
			var output bytes.Buffer
			err := Run(t.Context(), c, &output)
			if err == nil || output.Len() != 0 {
				t.Fatal("failed startup claimed ready", err, output.String())
			}
			if kind == "later_bind" {
				var op *net.OpError
				if !errors.As(err, &op) || op.Op != "listen" {
					t.Fatal("startup did not reach the later listener", err)
				}
			}
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("failed startup retained journal ownership", err)
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
