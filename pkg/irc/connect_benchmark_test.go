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

// BenchmarkConnect measures one complete connection, registration and close
// over loopback TCP: the per-job session a gateway opens, and the credential
// paths that count failures. Loopback peers are exempt from per-address
// limits, so this covers the per-connection work only.
func BenchmarkConnect(b *testing.B) {
	access, token, wrong := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	accounts := filepath.Join(b.TempDir(), "accounts")
	address := testserver.Start(b, server.Config{}, func(s *server.Server) error { return s.RestoreAccounts(accounts) })
	gated := testserver.Start(b, server.Config{}, func(s *server.Server) error { return s.EnableAccess(access) })
	created, err := irc.Dial(irc.Config{Addr: address, Nick: "member", IdentityToken: token, CreateAccount: true})
	if err != nil {
		b.Fatal(err)
	}
	created.Close()
	accept := func(b *testing.B, cfg irc.Config) {
		b.ReportAllocs()
		for b.Loop() {
			client, err := irc.Dial(cfg)
			if err != nil {
				b.Fatal(err)
			}
			client.Close()
		}
	}
	reject := func(b *testing.B, cfg irc.Config) {
		b.ReportAllocs()
		for b.Loop() {
			client, err := irc.Dial(cfg)
			var rejected *irc.RejectedError
			if !errors.As(err, &rejected) {
				if client != nil {
					client.Close()
				}
				b.Fatal("credential was not rejected:", err)
			}
		}
	}
	b.Run("guest_ephemeral", func(b *testing.B) {
		accept(b, irc.Config{Addr: address, Nick: "guest", Ephemeral: true})
	})
	b.Run("guest_persistent", func(b *testing.B) {
		// A persistent guest claims its nickname, so each needs its own.
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			client, err := irc.Dial(irc.Config{Addr: address, Nick: fmt.Sprintf("visitor%d", i)})
			if err != nil {
				b.Fatal(err)
			}
			client.Close()
			i++
		}
	})
	b.Run("account_sasl_ephemeral", func(b *testing.B) {
		accept(b, irc.Config{Addr: address, Nick: "member", IdentityToken: token, Ephemeral: true})
	})
	b.Run("access_pass", func(b *testing.B) {
		accept(b, irc.Config{Addr: gated, Nick: "guest", AccessToken: access, Ephemeral: true})
	})
	b.Run("wrong_access_pass", func(b *testing.B) {
		reject(b, irc.Config{Addr: gated, Nick: "guest", AccessToken: wrong, Ephemeral: true})
	})
	b.Run("wrong_sasl", func(b *testing.B) {
		reject(b, irc.Config{Addr: address, Nick: "member", IdentityToken: wrong, Ephemeral: true})
	})
}
