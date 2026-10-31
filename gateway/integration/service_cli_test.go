//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestGatewayServeCLIHTTPSPUTStatusAndJoinedShutdown(t *testing.T) {
	t.Parallel()
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	c, identity := acceptanceJournalFixture(t, socket, account)
	certificate := testtls.New(t)
	if _, err := auth.ServerCertificate(certificate.CertPath, certificate.KeyPath); err != nil {
		t.Fatalf("local certificate preparation: %v", err)
	}
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath, Operations: []core.Operation{core.Send, core.Reply, core.ReceiptGet}}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	token := fixtureAuthor + ":" + fixtureAccount
	transport := &http.Transport{TLSClientConfig: certificate.Client, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	r := core.Request{Version: 1, RequestID: "cli-post", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "real serve process"}
	request, err := http.NewRequest(http.MethodPut, "https://"+address+"/v1/requests/"+r.RequestID, bytes.NewReader(requestJSON(t, r)))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 202 || response.ProtoMajor != 2 {
		t.Fatal("CLI HTTPS admission failed", response.StatusCode, response.Proto)
	}
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	result := acceptedReceipt(t, 5*time.Second, 20*time.Millisecond, "the CLI to save acceptance", func() *core.Result {
		request, err = http.NewRequest(http.MethodGet, "https://"+address+"/v1/requests/"+r.RequestID, nil)
		if err != nil {
			t.Fatal(err)
		}
		setAuthorHeaders(request, token)
		response, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result core.Result
		err = json.NewDecoder(response.Body).Decode(&result)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatal("CLI status failed", err, response.StatusCode)
		}
		return &result
	})
	if result.MessageID != seen.ID || seen.From != "fixture-author" || seen.Target != r.Target.Name {
		t.Fatal("CLI observer mismatch", result, seen)
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal("CLI shutdown failed", p.err, p.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("CLI shutdown did not join")
	}
	if conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("CLI listener survived shutdown")
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal("CLI journal ownership survived exit", err)
	}
	defer s.Close()
	j, err := s.LookupAuthor(context.Background(), identity, r.RequestID)
	if err != nil || j.Result.MessageID != seen.ID || j.Result.SendAttempts != 1 {
		t.Fatal("CLI durable result lost", j, err)
	}
}
