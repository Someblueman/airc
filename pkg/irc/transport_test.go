package irc_test

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestTLSAccessAndAccountReconnect(t *testing.T) {
	cert := testtls.New(t)
	token := strings.Repeat("a", 64)
	srv := server.New(server.Config{TLSConfig: cert.Server, HistoryLimit: 10, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := srv.EnableAccess(token); err != nil {
		t.Fatal(err)
	}
	if err := srv.RestoreAccounts(filepath.Join(t.TempDir(), "accounts.json")); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	base := irc.Config{Nick: "agent", Addr: listener.Addr().String(), TLSConfig: cert.Client, AccessToken: token, Ephemeral: true}
	for _, kind := range []string{"missing", "wrong", "unknown-ca", "wrong-host"} {
		t.Run(kind, func(t *testing.T) {
			cfg := base
			cfg.TLSConfig = cert.Client.Clone()
			switch kind {
			case "missing":
				cfg.AccessToken = ""
			case "wrong":
				cfg.AccessToken = strings.Repeat("b", 64)
			case "unknown-ca":
				cfg.TLSConfig = &tls.Config{}
			case "wrong-host":
				cfg.TLSConfig.ServerName = "other.example"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			client, err := irc.DialContext(ctx, cfg)
			if err == nil {
				client.Close()
				t.Fatal("unauthorized transport accepted")
			}
			if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), strings.Repeat("b", 64)) {
				t.Fatal("credential leaked in error")
			}
		})
	}
	base.IdentityToken, base.CreateAccount = strings.Repeat("c", 64), true
	first, err := irc.Dial(base)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Supports("TLS") || !first.Supports("ACCESS") || !first.Supports("ACCOUNTS") {
		t.Fatal(first.Features())
	}
	if err := first.Send("#room", "encrypted hello"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, first, func(e irc.Event) bool { _, ok := e.(*irc.SendReceiptEvent); return ok })
	first.Close()
	base.CreateAccount = false
	second, err := irc.Dial(base)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Raw("STATUS"); err != nil {
		t.Fatal(err)
	}
	event := nextEvent(t, second, func(e irc.Event) bool { r, ok := e.(*irc.RawEvent); return ok && r.Command == "770" }).(*irc.RawEvent)
	if !strings.Contains(event.Trailing, `"tls":true`) || !strings.Contains(event.Trailing, `"access_required":true`) {
		t.Fatal(event.Trailing)
	}
}

func TestPlainRemoteClientSendsNoCredentials(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var host string
	for _, addr := range addresses {
		ip, _, _ := net.ParseCIDR(addr.String())
		if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			host = ip.String()
			break
		}
	}
	if host == "" {
		t.Skip("no nonloopback IPv4 interface")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- nil
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(time.Second))
		data, _ := io.ReadAll(conn)
		received <- data
	}()
	client, err := irc.Dial(irc.Config{Nick: "agent", Addr: listener.Addr().String(), AccessToken: strings.Repeat("a", 64), IdentityToken: strings.Repeat("b", 64)})
	if err == nil {
		client.Close()
		t.Fatal("plaintext remote connection accepted")
	}
	if data := <-received; len(data) != 0 {
		t.Fatalf("sent %d plaintext bytes", len(data))
	}
}
