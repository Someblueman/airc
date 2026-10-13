package irc_test

import (
	"context"
	"crypto/tls"
	"io"
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
	address := startServer(t, server.Config{TLSConfig: cert.Server, HistoryLimit: 10}, func(srv *server.Server) error {
		if err := srv.EnableAccess(token); err != nil {
			return err
		}
		return srv.RestoreAccounts(filepath.Join(t.TempDir(), "accounts.json"))
	})
	base := irc.Config{Nick: "agent", Addr: address, TLSConfig: cert.Client, AccessToken: token, Ephemeral: true}
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

// A caller's dialer opens the connection; TLS is still layered on top and
// still verifies the server.
func TestDialContextHookCarriesTLS(t *testing.T) {
	cert := testtls.New(t)
	address := startServer(t, server.Config{TLSConfig: cert.Server}, nil)
	dials := 0
	cfg := irc.Config{Nick: "agent", Addr: address, TLSConfig: cert.Client, Ephemeral: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dials++
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := irc.DialContext(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if dials != 1 || !client.Supports("TLS") {
		t.Fatal(dials, client.Features())
	}
	cfg.TLSConfig = cert.Client.Clone()
	cfg.TLSConfig.ServerName = "other.example"
	if wrong, err := irc.DialContext(ctx, cfg); err == nil {
		wrong.Close()
		t.Fatal("hooked dial skipped certificate verification")
	}
}
