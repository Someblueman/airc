//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestGatewayServeCLIResumesJournalAfterOfflineRestart(t *testing.T) {
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, account)
	certificate := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath, Operations: []core.Operation{core.Send, core.ReceiptGet}}}
	if err := os.Rename(socket, socket+".offline"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(socket+".offline", socket) })
	binary := gatewayBinary(t)
	p := startGateway(t, binary, c)
	address := gatewayAddress(t, p)
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: certificate.Client, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	r := core.Request{Version: 1, RequestID: "cli-offline-restart", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "resume without caller resubmission"}
	request, err := http.NewRequest(http.MethodPost, "https://"+address+"/v1/messages", bytes.NewReader(requestJSON(t, r)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var admitted core.Result
	err = json.NewDecoder(response.Body).Decode(&admitted)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 202 || !admitted.Durable || response.ProtoMajor != 2 {
		t.Fatal("offline admission failed", admitted, response.StatusCode, err)
	}
	before := awaitHTTPResult(t, client, address, token, r.RequestID, func(result core.Result) bool { return result.State == core.RetryWait && result.SendAttempts > 0 })
	if before.AcceptanceUncertain || before.DuplicateRisk {
		t.Fatal("pre-dial outage created acceptance uncertainty", before)
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal("offline shutdown failed", p.err, p.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("offline shutdown did not join")
	}
	transport.CloseIdleConnections()
	p = startGateway(t, binary, c)
	address = gatewayAddress(t, p)
	reopened := awaitHTTPResult(t, client, address, token, r.RequestID, func(result core.Result) bool { return result.SendAttempts >= before.SendAttempts })
	if reopened.State == core.Accepted || !reopened.Durable || reopened.ExpiresAt != before.ExpiresAt {
		t.Fatal("restart lost durable pending state/defaults", reopened, before)
	}
	if err := os.Rename(socket+".offline", socket); err != nil {
		t.Fatal(err)
	}
	accepted := awaitHTTPResult(t, client, address, token, r.RequestID, func(result core.Result) bool { return result.State == core.Accepted })
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
	if seen.Message != r.Message || seen.From != "fixture-author" || seen.Target != r.Target.Name || accepted.SendAttempts <= before.SendAttempts || accepted.Accepted == nil || !*accepted.Accepted || accepted.DuplicateRisk {
		t.Fatal("restart delivery mismatch", accepted, seen)
	}
}

func awaitHTTPResult(t *testing.T, client *http.Client, address, token, id string, predicate func(core.Result) bool) core.Result {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		request, err := http.NewRequest(http.MethodGet, "https://"+address+"/v1/requests/"+id, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result core.Result
		err = json.NewDecoder(response.Body).Decode(&result)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatal("HTTP status failed", response.StatusCode, err)
		}
		if predicate(result) {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTP status did not reach required state", result)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
