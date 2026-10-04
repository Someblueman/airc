//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestGatewayServeCLIDetachedWithoutBearer(t *testing.T) {
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
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	certificate := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath, Operations: []core.Operation{core.Send, core.Reply, core.ReceiptGet}}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	transport := &http.Transport{TLSClientConfig: certificate.Client, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, encrypted := range []bool{false, true} {
		r := core.Request{Version: 1, RequestID: "cli-signed", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "actual signing-only CLI"}
		if encrypted {
			r.RequestID = "cli-encrypted"
			r.Message = "actual encrypted CLI"
		}
		route := "signed"
		if encrypted {
			route = "encrypted"
		}
		req, _ := http.NewRequest(http.MethodPost, "https://"+address+"/v1/envelopes/"+route, strings.NewReader(credentials.token(t, r, encrypted)))
		req.Header.Set("Content-Type", "application/jose")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result dispatch.DetachedResult
		err = json.NewDecoder(response.Body).Decode(&result)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 202 || response.ProtoMajor != 2 || result.Result == nil || !result.Result.Durable {
			t.Fatal("CLI detached admission failed", response.Status, err, result)
		}
		seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
		if seen.From != "fixture-author" {
			t.Fatal("CLI changed signer identity", seen)
		}
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal(p.err, p.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("signing-only CLI did not join")
	}
}
