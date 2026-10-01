package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestRemoteCLIProfilesAndReads(t *testing.T) {
	agentEnv(t)
	cert := testtls.New(t)
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "access.token")
	if err := admin.CreateToken(tokenPath); err != nil {
		t.Fatal(err)
	}
	token, _ := admin.ReadToken(tokenPath)
	srv := server.New(server.Config{TLSConfig: cert.Server, HistoryLimit: 10, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := srv.EnableAccess(token); err != nil {
		t.Fatal(err)
	}
	if err := srv.RestoreAccounts(filepath.Join(dir, "accounts.json")); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	flags := []string{"--addr", listener.Addr().String(), "--tls-ca", cert.CAPath, "--access-token-file", tokenPath, "--nick", "claude"}
	var messageID string
	for _, command := range [][]string{{"user", "create", "--model", "Claude"}, {"send", "--channel", "#room", "--message", "remote CLI hello"}, {"history", "#room", "--json"}, {"follow", "#room"}, {"following", "--json"}, {"doctor", "--json"}} {
		if command[0] == "follow" {
			command[1] = messageID
		}
		var output bytes.Buffer
		args := append(append([]string{}, command...), flags...)
		if err := run(args, strings.NewReader(""), &output, &output); err != nil {
			t.Fatal(args[:len(command)], err, output.String())
		}
		if command[0] == "history" {
			if !strings.Contains(output.String(), "remote CLI hello") {
				t.Fatal(output.String())
			}
			var row struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(output.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			messageID = row.ID
		}
		if command[0] == "doctor" {
			var report doctorReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil || !report.Server.TLS || !report.Server.AccessRequired || !strings.HasPrefix(report.Address, "tls://") {
				t.Fatal(output.String(), err)
			}
		}
	}
	opt := options{addr: listener.Addr().String(), nick: "claude", tls: true}
	secured, _ := identityPath(opt)
	opt.tls = false
	plain, _ := identityPath(opt)
	if secured == plain {
		t.Fatal("TLS and plaintext identities share a credential path")
	}
}

func TestTransportOptionFailureBoundaries(t *testing.T) {
	agentEnv(t)
	for _, opt := range []options{{unix: "socket", tls: true}, {tlsCA: filepath.Join(t.TempDir(), "missing")}, {accessTokenFile: filepath.Join(t.TempDir(), "missing")}} {
		if _, err := transportConfig(opt); err == nil {
			t.Fatal("accepted bad transport", opt)
		}
	}
	t.Setenv("AIRC_TLS", "invalid")
	if _, err := transportConfig(options{}); err == nil {
		t.Fatal("silently disabled TLS")
	}
}

func TestTLSHostnameScopesIdentityAndCursors(t *testing.T) {
	agentEnv(t)
	first := options{addr: "127.0.0.1:16667", nick: "claude", tlsServerName: "first.example"}
	second := first
	second.tlsServerName = "second.example"
	firstIdentity, _ := identityPath(first)
	secondIdentity, _ := identityPath(second)
	firstCursor, _, _ := cursorPath(first, "claude")
	secondCursor, _, _ := cursorPath(second, "claude")
	if firstIdentity == secondIdentity || firstCursor == secondCursor {
		t.Fatal("different TLS peers share local credentials or cursors")
	}
	second.tlsServerName = "FIRST.EXAMPLE"
	if serverKey(first) != serverKey(second) {
		t.Fatal("hostname case changed identity")
	}
}
