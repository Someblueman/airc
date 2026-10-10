//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSAuthenticationFrame(t *testing.T) {
	v, token := amqpVerifierFixture(t)
	routes := []config.NATSRoute{{Principal: "producer"}}
	for _, wire := range []string{string(natsConnectBytes(token)), `CONNECT {"auth_token":"` + token + `"}` + "\r\n"} {
		f, err := natswire.Read(bufio.NewReader(strings.NewReader(wire)))
		if err != nil {
			t.Fatal(err)
		}
		id, verbose, err := authenticateNATSFrame(v, f, routes)
		if err != nil || id.ID() != "producer" || verbose != !strings.Contains(wire, `"verbose":false`) {
			t.Fatalf("identity/options: %v", err)
		}
	}
	base := `{"auth_token":"` + token + `"`
	for _, suffix := range []string{
		`,"user":""}`, `,"pass":"secret"}`, `,"jwt":""}`, `,"nkey":"key"}`, `,"sig":"signature"}`,
		`,"headers":true}`, `,"no_responders":true}`, `,"protocol":-1}`, `,"protocol":2}`, `,"protocol":1.5}`,
		`,"name":"` + strings.Repeat("a", 256) + `"}`, `,"lang":"x\n"}`, `,"version":"x\u0000"}`,
		`,"auth_token":"duplicate"}`, `,"verbose":null}`, `,"verbose":"false"}`, `,"Auth_token":"alias"}`, `,"unknown":"secret"}`,
	} {
		_, _, err := authenticateNATSFrame(v, natswire.Frame{Command: "CONNECT", JSON: []byte(base + suffix)}, routes)
		if !errors.Is(err, auth.ErrAuthentication) || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("refusal: %v", err)
		}
	}
	for _, tc := range []struct {
		token   string
		routes  []config.NATSRoute
		command string
	}{
		{strings.Repeat("0", 64), routes, "CONNECT"}, {token, []config.NATSRoute{{Principal: "other"}}, "CONNECT"}, {token, nil, "CONNECT"}, {token, routes, "PUB"},
	} {
		if _, _, err := authenticateNATSFrame(v, natswire.Frame{Command: tc.command, JSON: []byte(`{"auth_token":"` + tc.token + `"}`)}, tc.routes); err == nil {
			t.Fatal("accepted unauthorized CONNECT")
		}
	}
}

func TestNATSTLSAuthenticationPreservesPipeline(t *testing.T) {
	client, _, done, token := natsAuthFixture(t, tls.VersionTLS13)
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	wire := append(natsConnectBytes(token), []byte("PING\r\nSUB airc.response 1\r\n")...)
	written := make(chan error, 1)
	go func() { _, err := client.Write(wire); written <- err }()
	r := natsAuthFinished(t, done)
	if r.err != nil || r.session.identity.ID() != "producer" || r.session.verbose || r.session.conn.ConnectionState().Version != tls.VersionTLS13 {
		t.Fatalf("authentication: %v", r.err)
	}
	for _, command := range []string{"PING", "SUB"} {
		f, err := natswire.Read(r.session.reader)
		if err != nil || f.Command != command {
			t.Fatalf("pipeline: %s %v", f.Command, err)
		}
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}
