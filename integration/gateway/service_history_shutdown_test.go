//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestGatewayServeCLIShutdownJoinsActiveReadLease(t *testing.T) {
	for _, op := range []core.Operation{core.HistoryRead, core.Search, core.Watch, core.Directory} {
		t.Run(string(op), func(t *testing.T) { cliReadShutdown(t, op) })
	}
}

func cliReadShutdown(t *testing.T, op core.Operation) {
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	mode, route := "withhold-history", "/v1/history?kind=room&target=%23gateway-fixture&limit=1"
	if op == core.Search {
		mode, route = "withhold-search", "/v1/search?kind=room&target=%23gateway-fixture&limit=1&query=x"
	}
	if op == core.Watch {
		route = "/v1/watch?kind=room&target=%23gateway-fixture&limit=1"
	}
	if op == core.Directory {
		mode, route = "withhold-directory", "/v1/directory?limit=256"
	}
	proxy, joined, _, checks := backendProxy(t, socket, mode, nil)
	c, _ := acceptanceJournalFixture(t, proxy, account)
	c.Principals[0].Operations = append(c.Principals[0].Operations, op)
	if op != core.Directory {
		c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
		if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
			t.Fatal(err)
		}
	}
	certificate := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath, Operations: []core.Operation{op}}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: certificate.Client, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, err := http.NewRequest(http.MethodGet, "https://"+address+route, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	result := make(chan int, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := client.Do(request)
		if err != nil {
			result <- 0
			return
		}
		_ = response.Body.Close()
		result <- response.StatusCode
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("owned HTTP caller survived cleanup")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for checks.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("CLI read did not issue snapshot command")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal("active-read shutdown failed", p.err, p.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("active-read shutdown did not join")
	}
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream read survived CLI exit")
	}
	select {
	case status := <-result:
		if status == 200 {
			t.Fatal("withheld snapshot returned success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP caller did not return after CLI exit")
	}
	if checks.Load() != 1 {
		t.Fatal("shutdown retried/reconnected history", checks.Load())
	}
	transport.CloseIdleConnections()
	c.Listeners[0].Address = address
	rebound := startGateway(t, gatewayBinary(t), c)
	if got := gatewayAddress(t, rebound); got != address {
		t.Fatal("active-read shutdown did not release the same listener address", got, address)
	}
	request, err = http.NewRequest(http.MethodGet, "https://"+address+"/v1/capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("rebound listener unavailable", err)
	}
	var capabilities dispatch.Capabilities
	err = json.NewDecoder(response.Body).Decode(&capabilities)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || response.ProtoMajor != 2 || !slices.Contains(capabilities.Operations, op) {
		t.Fatal("rebound listener lost authenticated read capability", response.StatusCode, capabilities, err)
	}
}
