//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func cliReadCursorRestart(t *testing.T, op core.Operation) {
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, socket, "fixture-author", account, true, true)
	var receipts []*irc.SendReceiptEvent
	for _, body := range []string{"history one\nwhole body", "history two", "history three"} {
		if err := author.SendWithID("#gateway-fixture", body, strings.ReplaceAll(body[:11], " ", "-")+string(rune('a'+len(receipts)))); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Message == body }))
	}
	c, _ := acceptanceJournalFixture(t, socket, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Principals[0].Operations = append(c.Principals[0].Operations, op)
	c.Principals[0].Targets = append(c.Principals[0].Targets, core.Target{Kind: "room", Name: "#other-allowed"})
	other := c.Principals[0]
	other.ID = "other"
	other.BearerTokenFile = filepath.Join(filepath.Dir(c.StateDir), "other.token")
	if err := auth.CreateCredential(other.BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	c.Principals = append(c.Principals, other)
	certificate := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "https", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath, Operations: []core.Operation{op}}}
	binary := gatewayBinary(t)
	p := startGateway(t, binary, c)
	address := gatewayAddress(t, p)
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := auth.ReadToken(other.BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: certificate.Client, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"1"}}
	call := func(query, credential string) (int, []byte) {
		t.Helper()
		route := "/v1/history?"
		if op == core.Search {
			route = "/v1/search?query=history&"
		}
		r, err := http.NewRequest(http.MethodGet, "https://"+address+route+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+credential)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if err != nil || response.ProtoMajor != 2 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("history transport failed", response.Proto, err)
		}
		return response.StatusCode, data
	}
	read := func() backend.HistoryPage {
		t.Helper()
		status, data := call(query.Encode(), token)
		var page backend.HistoryPage
		if err := json.Unmarshal(data, &page); err != nil || status != 200 {
			t.Fatal("history failed", status, string(data), err)
		}
		return page
	}
	first := read()
	if len(first.Messages) != 1 || first.Messages[0].ID != receipts[0].ID || first.Messages[0].Message != receipts[0].Message || first.Status != "more" || !strings.HasPrefix(first.Cursor, "gc1.") || first.Gap {
		t.Fatal("first external page mismatch", first)
	}
	query.Set("cursor", first.Cursor)
	second := read()
	if len(second.Messages) != 1 || second.Messages[0].ID != receipts[1].ID || second.Status != "more" {
		t.Fatal("second external page mismatch", second)
	}
	for _, test := range []struct {
		query, token string
		status       int
	}{
		{query.Encode(), otherToken, 400},
		{"kind=room&target=%23other-allowed&limit=1&cursor=" + url.QueryEscape(first.Cursor), token, 400},
		{"kind=room&target=%23gateway-fixture&cursor=" + receipts[0].ID, token, 400},
		{"kind=room&target=%23gateway-fixture&cursor=" + url.QueryEscape(first.Cursor) + "x", token, 400},
		{"kind=room&target=%23gateway-fixture&limit=1&limit=2", token, 400},
		{"kind=room&target=%23gateway-fixture&unknown=x", token, 400},
		{query.Encode() + "&start=recent", token, 400},
		{"kind=room&target=%23gateway-fixture&start=invalid", token, 400},
		{"kind=room&target=%23gateway-%ff", token, 400},
		{"kind=room&target=%23private", token, 403},
		{query.Encode(), strings.Repeat("0", 64), 401},
	} {
		if status, data := call(test.query, test.token); status != test.status {
			t.Fatal("history denial failed", status, test.status, string(data))
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
		t.Fatal("history process shutdown did not join")
	}
	transport.CloseIdleConnections()
	p = startGateway(t, binary, c)
	address = gatewayAddress(t, p)
	query.Set("cursor", second.Cursor)
	third := read()
	if len(third.Messages) != 1 || third.Messages[0].ID != receipts[2].ID || third.Status != "ok" || third.Gap {
		t.Fatal("cursor did not survive real process restart", third)
	}
	query.Set("cursor", third.Cursor)
	if empty := read(); len(empty.Messages) != 0 || empty.Status != "ok" {
		t.Fatal("caught-up cursor repeated history", empty)
	}
}
