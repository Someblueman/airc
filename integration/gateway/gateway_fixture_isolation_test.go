//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

type isolatedGateway struct {
	process                *gatewayProcess
	client                 *http.Client
	token, address, native string
}

func newIsolatedGateway(t *testing.T, binary string) isolatedGateway {
	t.Helper()
	native := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, native, account)
	cert := testtls.New(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Send, core.ReceiptGet}}}
	p := startGateway(t, binary, c)
	address := gatewayAddress(t, p)
	token := fixtureAuthor + ":" + fixtureAccount
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cert.Client}, Timeout: 5 * time.Second}
	t.Cleanup(client.Transport.(*http.Transport).CloseIdleConnections)
	return isolatedGateway{process: p, client: client, token: token, address: address, native: native}
}

func isolatedCall(t *testing.T, g isolatedGateway, method, id, token string, request *core.Request) (int, core.Result) {
	t.Helper()
	var body *bytes.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	} else {
		body = bytes.NewReader(nil)
	}
	path := "https://" + g.address + "/v1/requests/" + id
	r, err := http.NewRequest(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(r, token)
	r.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result core.Result
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, result
}

func TestCurrentGatewayBinaryKeepsFixturesIsolated(t *testing.T) {
	binary := currentGatewayBinary(t)
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal("read gateway build metadata", err)
	}
	childRace := false
	for _, setting := range info.Settings {
		if setting.Key == "-race" {
			childRace = setting.Value == "true"
		}
	}
	if childRace != gatewayTestRace {
		t.Fatalf("gateway race instrumentation=%t, parent=%t", childRace, gatewayTestRace)
	}
	first, second := newIsolatedGateway(t, binary), newIsolatedGateway(t, binary)
	one := core.Request{Version: 1, RequestID: "shared-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "first fixture"}
	two := one
	two.Message = "second fixture"
	if status, _ := isolatedCall(t, first, http.MethodPut, one.RequestID, first.token, &one); status != http.StatusAccepted {
		t.Fatal("first admission", status)
	}
	if status, _ := isolatedCall(t, second, http.MethodPut, two.RequestID, second.token, &two); status != http.StatusAccepted {
		t.Fatal("second admission", status)
	}
	var r1, r2 core.Result
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, r1 = isolatedCall(t, first, http.MethodGet, one.RequestID, first.token, nil)
		_, r2 = isolatedCall(t, second, http.MethodGet, two.RequestID, second.token, nil)
		if r1.State == core.Accepted && r2.State == core.Accepted {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if r1.State != core.Accepted || r2.State != core.Accepted || r1.MessageID == "" || r1.MessageID == r2.MessageID {
		t.Fatalf("isolated results: %#v %#v", r1, r2)
	}
	firstHistory := topicHistorySnapshot(t, first.native)
	secondHistory := topicHistorySnapshot(t, second.native)
	if firstHistory[r1.MessageID] != one.Message || firstHistory[r2.MessageID] != "" || secondHistory[r2.MessageID] != two.Message || secondHistory[r1.MessageID] != "" {
		t.Fatalf("native histories crossed: first=%v second=%v", firstHistory, secondHistory)
	}
	if status, _ := isolatedCall(t, second, http.MethodGet, two.RequestID, wrongAuthorSpec, nil); status != http.StatusUnauthorized {
		t.Fatalf("receipt with a wrong token status=%d", status)
	}
	if err := first.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.process.done:
	case <-time.After(8 * time.Second):
		t.Fatal("first gateway did not join")
	}
	if first.process.err != nil {
		t.Fatalf("first gateway shutdown: %v %s", first.process.err, first.process.stderr.String())
	}
	three := one
	three.RequestID = "second-after-stop"
	three.Message = "second remains alive"
	if status, _ := isolatedCall(t, second, http.MethodPut, three.RequestID, second.token, &three); status != http.StatusAccepted {
		t.Fatal("second after first stop", status)
	}
	deadline = time.Now().Add(5 * time.Second)
	var r3 core.Result
	for time.Now().Before(deadline) {
		_, r3 = isolatedCall(t, second, http.MethodGet, three.RequestID, second.token, nil)
		if r3.State == core.Accepted {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if r3.State != core.Accepted || r3.MessageID == "" {
		t.Fatalf("second did not finish after first stop: %#v", r3)
	}
	secondHistory = topicHistorySnapshot(t, second.native)
	if secondHistory[r3.MessageID] != three.Message || secondHistory[r1.MessageID] != "" {
		t.Fatalf("second history after first stop crossed state: %v", secondHistory)
	}
}
