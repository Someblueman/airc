package irc_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestGatewayConnectionsAssertTheirClientSource(t *testing.T) {
	access, gateway := strings.Repeat("a", 64), strings.Repeat("9", 64)
	address := startServer(t, server.Config{}, func(srv *server.Server) error {
		if err := srv.EnableAccess(access); err != nil {
			return err
		}
		if err := srv.EnableGateway(gateway); err != nil {
			return err
		}
		return srv.RestoreAccounts(filepath.Join(t.TempDir(), "accounts.json"))
	})
	register := func(nick, source string) error {
		cfg := irc.Config{Nick: nick, Addr: address, AccessToken: access, Ephemeral: true, IdentityToken: strings.Repeat("c", 64), CreateAccount: true}
		if source != "" {
			cfg.GatewayToken, cfg.Source = gateway, source
		}
		client, err := irc.Dial(cfg)
		if err != nil {
			return err
		}
		defer client.Close()
		if source != "" && !client.Supports("WEBIRC") {
			t.Fatal(client.Features())
		}
		return nil
	}
	// The ninth account from one source in an hour is refused, however many
	// gateway connections it arrives on; other sources and local clients are not.
	for i := range 8 {
		if err := register(fmt.Sprintf("user%d", i), "listener:mqtt"); err != nil {
			t.Fatal(err)
		}
	}
	var rejected *irc.RejectedError
	if err := register("over", "listener:mqtt"); !errors.As(err, &rejected) || rejected.Code != "437" {
		t.Fatalf("source limit not applied: %v", err)
	}
	if err := register("other", "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if err := register("local", ""); err != nil {
		t.Fatal(err)
	}

	_, err := irc.Dial(irc.Config{Nick: "x", Addr: address, AccessToken: access, Ephemeral: true, GatewayToken: strings.Repeat("8", 64), Source: "203.0.113.9"})
	if !errors.As(err, &rejected) || rejected.Code != "464" {
		t.Fatalf("wrong gateway credential: %v", err)
	}
	if _, err := irc.Dial(irc.Config{Nick: "x", Addr: address, Source: "203.0.113.9"}); err == nil {
		t.Fatal("source without a gateway credential accepted")
	}
	if _, err := irc.Dial(irc.Config{Nick: "x", Addr: address, GatewayToken: gateway, Source: "203.0.113.9:80"}); err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestGatewayConnectionRefusedWithoutServerSupport(t *testing.T) {
	address := startServer(t, server.Config{}, nil)
	_, err := irc.Dial(irc.Config{Nick: "x", Addr: address, Ephemeral: true, GatewayToken: strings.Repeat("9", 64), Source: "203.0.113.9"})
	var rejected *irc.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("a server without WEBIRC must not silently treat the client as local: %v", err)
	}
}
