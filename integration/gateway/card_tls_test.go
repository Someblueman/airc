//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestProfileAndPresenceOverFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := framedTLSCardFixture(t)
	defer conn.Close()
	defer stop()
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			if response.Error != nil {
				t.Fatalf("TLS card response correlation/event mismatch: correlation=%q event=%q code=%q failure=%+v", response.CorrelationID, response.Event, response.Error.Code, response.Error)
			}
			t.Fatalf("TLS card response correlation/event mismatch: %+v", response)
		}
		return response
	}
	model, workspace, tools, about := "tls model", "tls workspace", "tls tools", "TLS profile 🙂"
	profile := core.Request{Version: 1, RequestID: "tls-card-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model, Workspace: &workspace, Tools: &tools, About: &about}}
	got := assertCardMutation(t, call, socket, profile)
	if got.Card.Model != model || got.Card.Workspace != workspace || got.Card.Tools != tools || got.Card.About != about {
		t.Fatalf("TLS profile acknowledgement lost requested fields: %+v", got)
	}
	presence := core.Request{Version: 1, RequestID: "tls-card-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "running", Note: "TLS presence", TTLSeconds: 60}}
	got = assertCardMutation(t, call, socket, presence)
	if got.Card.State != "running" || got.Card.Note != "TLS presence" || got.Card.ExpiresAt.IsZero() {
		t.Fatalf("TLS presence acknowledgement lost requested fields: %+v", got)
	}
	clear := core.Request{Version: 1, RequestID: "tls-card-presence-clear", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "clear"}}
	got = assertCardMutation(t, call, socket, clear)
	if got.Card.State != "unknown" || got.Card.Note != "" || !got.Card.ExpiresAt.IsZero() {
		t.Fatalf("TLS presence clear acknowledgement was not empty: %+v", got)
	}
}

func framedTLSCardFixture(t *testing.T) (net.Conn, *detachedCredentials, func(), string) {
	t.Helper()
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	certificate := testtls.New(t)
	listener := config.Listener{ID: "stream", Protocol: "framed", Address: "127.0.0.1:0", Enabled: true, Operations: dispatch.ImplementedOperations(), TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath}
	c.Listeners = []config.Listener{listener}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	trust := certificate.Client.Clone()
	trust.NextProtos = []string{adapter.FramedALPN}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, trust)
	if err != nil {
		t.Fatal(err)
	}
	stop := func() {
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err, p.stderr.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("TLS gateway did not join")
		}
	}
	return conn, credentials, stop, socket
}
