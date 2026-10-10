//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

type uartProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
	stderr  bytes.Buffer
}

type uartFixture struct {
	process     *uartProcess
	pty         *ptyBroker
	connection  *tls.Conn
	config      config.Config
	credentials *detachedCredentials
	socket      string
	observer    *irc.Client
}

func newUARTFixture(t *testing.T) *uartFixture {
	t.Helper()
	pty := newPTYBroker(t)
	native := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	c.Principals[0].Operations = ops
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	certificate := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "uart", Protocol: "uart", Address: pty.path, Enabled: true, TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath, Operations: ops}}
	process := startUARTProcess(t, gatewayBinary(t), c)
	trust := certificate.Client.Clone()
	trust.ServerName = "localhost"
	trust.NextProtos = []string{adapter.FramedALPN}
	pty.waitReady(t, process)
	client := tls.Client(&ptyConn{file: pty.master}, trust)
	handshakeCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	err := client.HandshakeContext(handshakeCtx)
	cancel()
	if err != nil {
		select {
		case <-process.done:
			t.Fatal("UART TLS/ALPN handshake", err, process.err, process.stderr.String())
		case <-time.After(2 * time.Second):
			t.Fatal("UART TLS/ALPN handshake", err)
		}
	}
	state := client.ConnectionState()
	if state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != adapter.FramedALPN {
		t.Fatal("UART TLS/ALPN mismatch", state)
	}
	observer := dial(t, native, "uart-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "uart-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	t.Cleanup(func() { _ = client.Close() })
	return &uartFixture{process: process, pty: pty, connection: client, config: c, credentials: credentials, socket: native, observer: observer}
}

func startUARTProcess(t *testing.T, binary string, c config.Config) *uartProcess {
	t.Helper()
	root := filepath.Dir(c.StateDir)
	path := filepath.Join(root, "uart-gateway.json")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p := &uartProcess{command: exec.Command(binary, "serve-uart", "--file", path), done: make(chan struct{})}
	p.command.Env = append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+root, "XDG_STATE_HOME="+root)
	p.command.Stdout = io.Discard
	p.command.Stderr = &p.stderr
	if err := p.command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = p.command.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
			return
		default:
		}
		_ = p.command.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
		case <-time.After(8 * time.Second):
			_ = p.command.Process.Kill()
			<-p.done
			t.Error("UART service did not join after SIGINT")
		}
	})
	return p
}

func (p *uartProcess) stop(t *testing.T) {
	t.Helper()
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal("UART service SIGINT", p.err, p.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("UART service did not join SIGINT")
	}
}
