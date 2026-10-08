//go:build unix

package auth

import (
	"crypto/tls"
	"encoding/xml"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/xmppwire"
	"github.com/Someblueman/airc/internal/testtls"
)

func xmppSessionFixture(t *testing.T, mode string) (*XMPPServer, chan struct{}) {
	t.Helper()
	cert := testtls.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	done := make(chan struct{})
	entered := make(chan struct{})
	security := cert.Server.Clone()
	if mode == "tls12" {
		security.MinVersion = tls.VersionTLS12
		security.MaxVersion = tls.VersionTLS12
	}
	go func() {
		defer close(done)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- raw
		defer raw.Close()
		if mode != "blocked_sasl" {
			close(entered)
		}
		if mode == "blocked_tls" {
			_, _ = io.Copy(io.Discard, raw)
			return
		}
		conn := tls.Server(raw, security)
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if conn.Handshake() != nil {
			return
		}
		r := xmppwire.NewStream(conn)
		if _, err := r.Open(); err != nil {
			return
		}
		root := `<s:stream xmlns:s="http://etherx.jabber.org/streams" xmlns="jabber:client" from="localhost" version="1.0" id="first">`
		features := `<s:features><mechanisms xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><mechanism>PLAIN</mechanism></mechanisms></s:features>`
		if _, err = io.WriteString(conn, root+features); err != nil {
			return
		}
		if _, err = r.Read(); err != nil {
			return
		}
		if mode == "blocked_sasl" {
			close(entered)
			_, _ = io.Copy(io.Discard, conn)
			return
		}
		success := `<success xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`
		if mode == "failure" {
			success = `<failure xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`
		}
		if _, err = io.WriteString(conn, success); err != nil || mode == "failure" {
			return
		}
		if r.Restart() != nil {
			return
		}
		if _, err = r.Open(); err != nil {
			return
		}
		if _, err = io.WriteString(conn, root+`<s:features><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"/></s:features>`); err != nil {
			return
		}
		request, err := r.Read()
		if err != nil {
			return
		}
		id := ""
		for _, a := range request.Attributes {
			if a.Name == (xml.Name{Local: "id"}) {
				id = a.Value
			}
		}
		jid := "bridge@localhost/airc"
		if mode == "wrong_account" {
			jid = "other@localhost/airc"
		}
		_, err = io.WriteString(conn, `<iq type="result" id="`+id+`"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"><jid>`+jid+`</jid></bind></iq>`)
		if err != nil {
			return
		}
		xmppJoinScript(conn, r, mode)
		_, _ = io.Copy(io.Discard, conn)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case raw := <-accepted:
			_ = raw.Close()
		default:
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("XMPP fixture not joined")
		}
	})
	c, _ := fixtureConfig(t)
	c.Principals[0].Operations = []core.Operation{core.Send}
	password := filepath.Join(privateTemp(t), "xmpp-password")
	if err = os.WriteFile(password, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.XMPPBridge{ID: "xmpp", Address: listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Account: "bridge@localhost", Resource: "airc", PasswordFile: password, FreshnessSeconds: 60,
		Routes: []config.XMPPRoute{{Author: "alice@localhost", Room: "room@muc.localhost", Principal: "device", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	if mode == "unknown_ca" {
		b.TLSCAFile = testtls.New(t).CAPath
	}
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewXMPPServer(v, c, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, entered
}
