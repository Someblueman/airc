package irc_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/pkg/irc"
)

// BenchmarkGatewayConnect is BenchmarkConnect for a gateway's per-job
// session: WEBIRC first, so the per-address limits and the failed-credential
// throttle apply to each asserted source.
func BenchmarkGatewayConnect(b *testing.B) {
	gateway, token, wrong := strings.Repeat("9", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	accounts := filepath.Join(b.TempDir(), "accounts")
	address := testserver.Start(b, server.Config{}, func(s *server.Server) error {
		if err := s.RestoreAccounts(accounts); err != nil {
			return err
		}
		return s.EnableGateway(gateway)
	})
	created, err := irc.Dial(irc.Config{Addr: address, Nick: "member", IdentityToken: token, CreateAccount: true})
	if err != nil {
		b.Fatal(err)
	}
	created.Close()
	// source cycles through n distinct IPv4 sources.
	source := func(i, n int) string { i %= n; return fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255) }
	b.Run("guest_ephemeral", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			client, err := irc.Dial(irc.Config{Addr: address, Nick: "guest", Ephemeral: true, GatewayToken: gateway, Source: source(i, 1<<20)})
			if err != nil {
				b.Fatal(err)
			}
			client.Close()
			i++
		}
	})
	b.Run("account_sasl_ephemeral", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			client, err := irc.Dial(irc.Config{Addr: address, Nick: "member", IdentityToken: token, Ephemeral: true, GatewayToken: gateway, Source: source(i, 1<<20)})
			if err != nil {
				b.Fatal(err)
			}
			client.Close()
			i++
		}
	})
	fail := func(b *testing.B, source string) {
		_, err := irc.Dial(irc.Config{Addr: address, Nick: "member", IdentityToken: wrong, Ephemeral: true, GatewayToken: gateway, Source: source})
		var rejected *irc.RejectedError
		if !errors.As(err, &rejected) {
			b.Fatal("credential was not rejected:", err)
		}
	}
	// One source past its failure budget: denied without checking.
	b.Run("wrong_sasl_throttled", func(b *testing.B) {
		for range 10 {
			fail(b, "192.0.2.1")
		}
		b.ReportAllocs()
		for b.Loop() {
			fail(b, "192.0.2.1")
		}
	})
	// Every failure from a new source once the table holds its bound of 4096
	// addresses: each one sweeps the whole table under the server lock.
	b.Run("wrong_sasl_new_source_table_full", func(b *testing.B) {
		for i := range 4096 {
			fail(b, source(i, 1<<16))
		}
		b.ReportAllocs()
		i := 1 << 16
		for b.Loop() {
			fail(b, source(i, 1<<24))
			i++
		}
	})
}
