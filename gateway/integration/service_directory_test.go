//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestGatewayServeCLIDirectoryWithoutCursorOrChatTargets(t *testing.T) {
	t.Parallel()
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	if err := provision.UpdateProfile(map[string]string{"about": "public CLI fixture card 🙂"}); err != nil {
		t.Fatal(err)
	}
	next(t, provision, func(e *irc.EndOfDirectoryEvent) bool { return true })
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, account)
	if c.CursorKeyFile != "" {
		t.Fatal("fixture unexpectedly supplies a cursor key")
	}
	certificate := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath, Operations: []core.Operation{core.Directory}}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	token := fixtureAuthor + ":" + fixtureAccount
	transport := &http.Transport{TLSClientConfig: certificate.Client, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, nick := range []string{"", "FiXtUrE-AuThOr", "unpublished-cli"} {
		route := "https://" + address + "/v1/directory?limit=1"
		if nick != "" {
			route += "&nick=" + nick
		}
		request, err := http.NewRequest(http.MethodGet, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		setAuthorHeaders(request, token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot backend.DirectorySnapshot
		err = json.NewDecoder(response.Body).Decode(&snapshot)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || response.ProtoMajor != 2 || snapshot.Version != 1 || len(snapshot.Agents) != 1 {
			t.Fatal("CLI public directory failed", nick, err, response.StatusCode, response.Proto, snapshot)
		}
		card := snapshot.Agents[0]
		if card.State != "unknown" || card.Connected {
			t.Fatal("CLI invented presence", card)
		}
		if nick == "unpublished-cli" {
			if card.Nick != nick || card.AccountID != "" || card.About != "" || !card.LastSeen.IsZero() {
				t.Fatal("CLI unknown query changed native card", card)
			}
		} else if card.Nick != "fixture-author" || card.About != "public CLI fixture card 🙂" {
			t.Fatal("CLI changed public native card", card)
		}
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
		t.Fatal("directory CLI did not join shutdown")
	}
}
