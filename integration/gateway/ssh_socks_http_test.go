//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
	"golang.org/x/net/proxy"
)

func TestHTTPDurableSendAndBoundedHistoryThroughSSHSOCKS(t *testing.T) {
	if os.Getenv("AIRC_TEST_SSH_SOCKS") != "1" {
		t.Skip("set AIRC_TEST_SSH_SOCKS=1 to run the isolated SSH SOCKS fixture")
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal("AIRC_TEST_SSH_SOCKS=1 but OpenSSH client is unavailable")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Fatal("AIRC_TEST_SSH_SOCKS=1 but OpenSSH server is unavailable")
	}

	f := gatewayHTTPFixture(t, false)
	forward := sshForward(t, ssh, sshd, f.server.Listener.Addr().String(), sshSOCKS)

	base, ok := f.server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("HTTP fixture has no cloneable transport")
	}
	dialer, err := proxy.SOCKS5("tcp", forward.localAddress, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal("create SOCKS5 client", err)
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		t.Fatal("SOCKS5 dialer does not support request contexts")
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DialContext = contextDialer.DialContext
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	t.Cleanup(client.CloseIdleConnections)

	deniedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer deniedListener.Close()
	denyCtx, denyCancel := context.WithTimeout(t.Context(), 3*time.Second)
	deniedConn, denyErr := contextDialer.DialContext(denyCtx, "tcp", deniedListener.Addr().String())
	denyCancel()
	if denyErr == nil {
		_ = deniedConn.Close()
		t.Fatal("SOCKS forwarding reached a destination outside sshd PermitOpen")
	}

	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	requests := []core.Request{
		{Version: 1, RequestID: "ssh-socks-http-first", Operation: core.Send, Target: room, Message: "SSH SOCKS first message\nwhole body 🙂"},
		{Version: 1, RequestID: "ssh-socks-http-second", Operation: core.Send, Target: room, Message: "SSH SOCKS second message\ncomplete body 🌍"},
	}
	if status, _, _ := socksHTTPCall(t, client, f, http.MethodPost, "/v1/messages", "invalid-bearer", requestJSON(t, requests[0])); status != http.StatusUnauthorized {
		t.Fatalf("SOCKS HTTP accepted invalid credentials: status=%d", status)
	}
	if status, _, _ := socksHTTPCall(t, client, f, http.MethodPost, "/v1/messages", f.token, bytes.Repeat([]byte(" "), core.MaxEnvelopeBytes+1)); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("SOCKS HTTP accepted oversized input: status=%d", status)
	}

	var observed []*irc.MessageEvent
	for _, request := range requests {
		status, protocol, data := socksHTTPCall(t, client, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, request))
		var admitted core.Result
		if err := json.Unmarshal(data, &admitted); err != nil || status != http.StatusAccepted || protocol != 1 || !admitted.Durable || admitted.RequestID != request.RequestID {
			t.Fatalf("SOCKS HTTP admission status/protocol=%d/%d result=%+v err=%v", status, protocol, admitted, err)
		}
		job := awaitDelivery(t, f.journal, f.identity, request.RequestID, func(job store.Job) bool { return job.Result.State == core.Accepted })
		event := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == job.Result.MessageID })
		if event.ID != job.Result.MessageID || event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
			t.Fatalf("SOCKS HTTP native observer mismatch: %+v", event)
		}
		observed = append(observed, event)

		status, protocol, data = socksHTTPCall(t, client, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, request))
		var replay core.Result
		if err := json.Unmarshal(data, &replay); err != nil || status != http.StatusAccepted || protocol != 1 || !replay.Durable || replay.RequestID != request.RequestID || replay.MessageID != event.ID || replay.SendAttempts != 1 || replay.State != core.Accepted {
			t.Fatalf("SOCKS HTTP replay changed acceptance: status/protocol=%d/%d result=%+v err=%v", status, protocol, replay, err)
		}
	}

	check := dial(t, f.socket, "ssh-socks-native-check", "", false, true)
	defer check.Close()
	checkCtx, checkCancel := context.WithTimeout(t.Context(), 5*time.Second)
	entries, err := irc.RequestCheck(checkCtx, check, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: room.Name, After: "*", Limit: 16}}, IncludeOwn: true, MaxMessages: 16}, nil)
	checkCancel()
	if err != nil {
		t.Fatal("read independent native CHECK history", err)
	}
	native := make([]*irc.MessageMetadata, 0, len(requests))
	for _, entry := range entries {
		if entry.Kind == "message" && entry.Message != nil {
			native = append(native, entry.Message)
		}
	}
	if len(native) != len(requests) || len(observed) != len(requests) {
		t.Fatalf("native CHECK/observer counts=%d/%d want=%d entries=%+v", len(native), len(observed), len(requests), entries)
	}
	for i, event := range observed {
		if native[i].ID != event.ID || native[i].From != event.From || native[i].Target != event.Target || native[i].Message != event.Message || native[i].ID == "" {
			t.Fatalf("observer event %d differs from native CHECK: event=%+v native=%+v", i, event, native[i])
		}
	}

	query := url.Values{"kind": {"room"}, "target": {room.Name}, "limit": {"1"}, "start": {"retained"}}
	read := func() backend.HistoryPage {
		t.Helper()
		status, protocol, body := socksHTTPCall(t, client, f, http.MethodGet, "/v1/history?"+query.Encode(), f.token, nil)
		var page backend.HistoryPage
		if err := json.Unmarshal(body, &page); err != nil || status != http.StatusOK || protocol != 1 {
			t.Fatalf("SOCKS HTTP bounded history status/protocol=%d/%d body=%s err=%v", status, protocol, body, err)
		}
		return page
	}
	for i, record := range native {
		page := read()
		wantStatus := "more"
		if i == len(native)-1 {
			wantStatus = "ok"
		}
		if len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) || page.Status != wantStatus || page.Cursor == "" || page.Gap {
			t.Fatalf("SOCKS HTTP page %d differs from ordered native CHECK record: page=%+v native=%+v", i, page, record)
		}
		query.Del("start")
		query.Set("cursor", page.Cursor)
	}
	resumed := read()
	if len(resumed.Messages) != 0 || resumed.Status != "ok" || resumed.Cursor == "" || resumed.Gap {
		t.Fatalf("SOCKS HTTP bounded cursor did not remain caught up: %+v", resumed)
	}
}

func socksHTTPCall(t *testing.T, client *http.Client, f httpFixture, method, path, token string, body []byte) (int, int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("SOCKS HTTP request failed", err)
	}
	defer response.Body.Close()
	if response.TLS == nil || response.TLS.Version < tls.VersionTLS13 || len(response.TLS.VerifiedChains) == 0 {
		t.Fatal("SOCKS HTTP did not verify the TLS server")
	}
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxResponse {
		t.Fatalf("SOCKS HTTP response exceeded %d-byte read bound", maxResponse)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated SOCKS HTTP response is cacheable")
	}
	return response.StatusCode, response.ProtoMajor, data
}
