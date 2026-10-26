//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestGatewayServeCLIShutdownJoinsStreamingEventsLease(t *testing.T) {
	for _, profile := range []string{"author"} {
		t.Run(profile, func(t *testing.T) { gatewayCLIStreamShutdown(t, profile) })
	}
}

func gatewayCLIStreamShutdown(t *testing.T, profile string) {
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	proxy, joined, _, checks := backendProxy(t, socket, "watch", nil)
	c, _ := acceptanceJournalFixture(t, proxy, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	cert := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", AllowedOrigins: []string{"https://fixture.example"}, Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Watch}}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	transport := &http.Transport{TLSClientConfig: cert.Client, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, "https://"+address+"/v1/events?kind=room&target=%23gateway-fixture", nil)
	setAuthorHeaders(request, fixtureAuthor+":"+fixtureAccount)
	request.Header.Set("Origin", "https://fixture.example")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewScanner(response.Body)
	page, _ := ssePage(t, reader)
	if response.Header.Get("Access-Control-Allow-Origin") != "https://fixture.example" || response.StatusCode != 200 || response.ProtoMajor != 2 || len(page.Messages) != 0 {
		t.Fatal("CLI streaming setup failed", response.Status, page)
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
		t.Fatal("CLI failed to join streaming handler")
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("upstream stream survived CLI exit")
	}
	if checks.Load() != 1 {
		t.Fatal("streaming reconnected or rechecked", checks.Load())
	}
}
