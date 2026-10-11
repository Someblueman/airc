//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

const (
	aliceToken = "a11ce0000000000000000000000000000000000000000000000000000000a11c"
	wrongToken = "bad0000000000000000000000000000000000000000000000000000000000bad"
)

// Registration goes through the gateway to aircd, which limits it per real
// client source (WEBIRC); a connection identifies once and then posts as the
// account; aircd throttles repeated bad tokens from one source.
func TestAuthorRegisterIdentifyAndThrottle(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	watcher := dial(t, socket, "watcher", "", false, false)
	if err := watcher.Join("#lobby"); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" })
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "web", Protocol: "http"}, config.Listener{ID: "ws", Protocol: "websocket"}, config.Listener{ID: "local", Protocol: "unix"})
	g := startAuthorGateway(t, c)
	base := "http://" + g.addresses["web"]

	register := func(i int) (int, map[string]any) {
		var reply map[string]any
		nick, token := fmt.Sprintf("member%d", i), fmt.Sprintf("%064x", i+1)
		status := plainHTTP(t, http.MethodPost, base+"/v1/request", plainRequest(map[string]any{"request_id": "register-" + nick, "operation": "account.register"}, nick, token), nil, &reply)
		return status, reply
	}
	for i := range 8 {
		status, reply := register(i)
		account, _ := reply["account"].(map[string]any)
		if status != http.StatusOK || account == nil || len(account["account_id"].(string)) != 32 {
			t.Fatalf("registration %d: HTTP %d %v", i, status, reply)
		}
	}
	status, reply := register(8)
	failure, _ := reply["error"].(map[string]any)
	if status != http.StatusTooManyRequests || failure["code"] != string(core.CapacityExceeded) || failure["upstream_code"] != float64(437) {
		t.Fatalf("ninth registration from one source: HTTP %d %v", status, reply)
	}
	var registered struct {
		Account struct {
			AccountID string `json:"account_id"`
		} `json:"account"`
	}
	aliceRegistration := plainRequest(map[string]any{"request_id": "register-alice", "operation": "account.register"}, "alice", aliceToken)
	unix := dialFramedUnix(t, g.addresses["local"])
	if r := unix.call(t, "register", aliceRegistration); r.Event != "result" || r.Result.Account == nil || len(r.Result.Account.AccountID) != 32 {
		t.Fatalf("registration from another source: %+v %+v", r, r.Error)
	} else {
		registered.Account.AccountID = r.Result.Account.AccountID
	}

	// Identify once on a WebSocket; later requests on it post as the account.
	ws := dialWebSocket(t, g.addresses["ws"])
	if r := ws.call(t, "identify", plainRequest(map[string]any{"request_id": "identify", "operation": "account.identify"}, "alice", aliceToken)); r.Event != "result" || r.Result.Account == nil || r.Result.Account.AccountID != registered.Account.AccountID {
		t.Fatalf("identify: %+v %+v", r, r.Error)
	}
	if r := ws.call(t, "post", sendRequest("ws-identified", "#lobby", "posted after identify", "", "")); r.Event != "result" || r.Result.Result == nil {
		t.Fatalf("post after identify: %+v %+v", r, r.Error)
	}
	if result := waitReceipt(t, base, "ws-identified", authorHeaders("alice", aliceToken)); result.State != core.Accepted {
		t.Fatalf("identified post: %+v %+v", result, result.Failure)
	} else if seen := next(t, watcher, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID }); seen.From != "alice" {
		t.Fatalf("identified post authored as %q", seen.From)
	}
	// Another connection has no identified author.
	if r := dialWebSocket(t, g.addresses["ws"]).call(t, "anon", sendRequest("ws-anon", "#lobby", "x", "", "")); r.Event != "error" || r.Error.Code != core.InvalidRequest {
		t.Fatalf("unidentified connection posted: %+v", r)
	}

	// A bad token fails fast; after nine from one source in ten minutes aircd
	// refuses even the right token from that source, unchecked, so the
	// gateway answers "retry later" rather than "wrong token". Another source
	// is unaffected.
	identify := func(token string) (int, failureBody) {
		var failure failureBody
		status := plainHTTP(t, http.MethodPost, base+"/v1/request", plainRequest(map[string]any{"request_id": "identify", "operation": "account.identify"}, "alice", token), nil, &failure)
		return status, failure
	}
	for i := range 9 {
		if status, failure := identify(wrongToken); status != http.StatusUnauthorized || failure.Error.Code != core.AuthenticationFailed {
			t.Fatalf("bad token %d: HTTP %d %+v", i, status, failure)
		}
	}
	if status, failure := identify(aliceToken); status != http.StatusTooManyRequests || failure.Error.Code != core.CapacityExceeded {
		t.Fatalf("throttled source accepted: HTTP %d %+v", status, failure)
	}
	if r := unix.call(t, "identify", plainRequest(map[string]any{"request_id": "identify", "operation": "account.identify"}, "alice", aliceToken)); r.Event != "result" {
		t.Fatalf("another source throttled: %+v", r.Error)
	}
}

// Idempotency, receipts and cursors belong to the author, never to the
// carrier: a retry of one request ID on another carrier is one AIRC message,
// a history cursor resumes elsewhere, and a registered author's job parked by
// a gateway restart resumes when the request comes back with its token.
func TestAuthorStateCrossesCarriersAndRestarts(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	provision := dial(t, socket, "alice", aliceToken, true, true)
	_ = provision.Close()
	listeners := []config.Listener{{ID: "web", Protocol: "http"}, {ID: "ws", Protocol: "websocket"}}
	c := authorConfig(t, socket, gatewayToken, listeners...)
	g := startAuthorGateway(t, c)
	base := "http://" + g.addresses["web"]

	request := sendRequest("one-message", "#lobby", "sent on two carriers", "Visitor", "")
	if status := plainHTTP(t, http.MethodPost, base+"/v1/messages", request, nil, nil); status != http.StatusAccepted {
		t.Fatal("first carrier", status)
	}
	ws := dialWebSocket(t, g.addresses["ws"])
	if r := ws.call(t, "retry", request); r.Event != "result" || r.Result.Result == nil || r.Result.Result.RequestID != "one-message" {
		t.Fatalf("retry on another carrier: %+v %+v", r, r.Error)
	}
	if result := waitReceipt(t, base, "one-message", authorHeaders("visitor", "")); result.State != core.Accepted {
		t.Fatalf("cross-carrier job: %+v", result)
	}
	for _, text := range []string{"second", "third"} {
		plainHTTP(t, http.MethodPost, base+"/v1/messages", sendRequest(text, "#lobby", text, "Visitor", ""), nil, nil)
		waitReceipt(t, base, text, authorHeaders("visitor", ""))
	}
	if n := historyCount(t, base, "#lobby", "sent on two carriers", authorHeaders("visitor", "")); n != 1 {
		t.Fatalf("%d AIRC messages for one request ID", n)
	}

	// A cursor from HTTP history resumes over the WebSocket.
	var page struct {
		Messages []*irc.MessageMetadata `json:"messages"`
		Cursor   string                 `json:"cursor"`
	}
	if status := plainHTTP(t, http.MethodGet, base+"/v1/history?kind=room&target=%23lobby&limit=1&start=retained", nil, authorHeaders("visitor", ""), &page); status != http.StatusOK || len(page.Messages) != 1 || page.Messages[0].Message != "sent on two carriers" || page.Cursor == "" {
		t.Fatalf("first page: HTTP %d %+v", status, page)
	}
	resumed := ws.call(t, "resume", plainRequest(map[string]any{"request_id": "history", "operation": "history.read", "target": map[string]any{"kind": "room", "name": "#lobby"}, "limit": 1, "cursor": page.Cursor}, "Visitor", ""))
	if resumed.Event != "result" || resumed.Result.Page == nil || len(resumed.Result.Page.Messages) != 1 || resumed.Result.Page.Messages[0].Message != "second" {
		t.Fatalf("cursor did not resume on another carrier: %+v %+v", resumed.Result, resumed.Error)
	}
	// Another author cannot use that cursor.
	if r := ws.call(t, "stolen", plainRequest(map[string]any{"request_id": "history", "operation": "history.read", "target": map[string]any{"kind": "room", "name": "#lobby"}, "limit": 1, "cursor": page.Cursor}, "Mallory", "")); r.Event != "error" {
		t.Fatal("another author's cursor accepted")
	}
	g.stop(t)

	// A registered author's job that a restart finds unfinished waits for
	// its token: the first gateway cannot reach AIRC, the next one can.
	unreachable := c
	unreachable.Upstream = &config.Upstream{Network: "unix", Address: socket + ".missing", GatewayTokenFile: gatewayToken}
	first := startAuthorGateway(t, unreachable)
	parked := sendRequest("parked", "#lobby", "after a restart", "alice", aliceToken)
	if status := plainHTTP(t, http.MethodPost, "http://"+first.addresses["web"]+"/v1/messages", parked, nil, nil); status != http.StatusAccepted {
		t.Fatal("registered post", status)
	}
	time.Sleep(1500 * time.Millisecond) // at least one failed attempt
	first.stop(t)
	second := startAuthorGateway(t, c)
	base = "http://" + second.addresses["web"]
	if result := waitReceipt(t, base, "parked", authorHeaders("alice", aliceToken)); result.State != core.CredentialWait {
		t.Fatalf("restarted job: %+v", result)
	}
	ws = dialWebSocket(t, second.addresses["ws"])
	if r := ws.call(t, "resubmit", parked); r.Event != "result" {
		t.Fatalf("resubmission: %+v", r.Error)
	}
	if result := waitReceipt(t, base, "parked", authorHeaders("alice", aliceToken)); result.State != core.Accepted {
		t.Fatalf("resumed job: %+v %+v", result, result.Failure)
	}
}

// The same plain request works on the rest of the HTTP family: HTTP/2
// without TLS and server-sent events. JSON-RPC and MCP are suite carriers
// (TestCarrierSuite/jsonrpc, TestCarrierSuite/mcp_http).
func TestAuthorHTTPFamilyCarriers(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "web", Protocol: "http"})
	g := startAuthorGateway(t, c)
	base := "http://" + g.addresses["web"]

	// HTTP/2 with prior knowledge, no TLS.
	h2 := &http.Transport{Protocols: new(http.Protocols)}
	h2.Protocols.SetUnencryptedHTTP2(true)
	body, _ := json.Marshal(sendRequest("h2-post", "#lobby", "over h2c", "Visitor", ""))
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/request", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: h2}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("h2c: %s %d", resp.Proto, resp.StatusCode)
	}
	waitReceipt(t, base, "h2-post", authorHeaders("visitor", ""))

	// Server-sent events, resumable by event ID.
	sse, _ := http.NewRequest(http.MethodGet, base+"/v1/events?kind=room&target=%23lobby&limit=10&start=retained", nil)
	sse.Header.Set("AIRC-Nick", "Visitor")
	stream, err := http.DefaultClient.Do(sse)
	if err != nil {
		t.Fatal(err)
	}
	firstEvent, err := bufio.NewReader(stream.Body).ReadString('\n')
	_ = stream.Body.Close()
	if err != nil || stream.StatusCode != http.StatusOK || !strings.HasPrefix(stream.Header.Get("Content-Type"), "text/event-stream") || !strings.HasPrefix(firstEvent, "retry:") {
		t.Fatalf("SSE: %d %q %v", stream.StatusCode, firstEvent, err)
	}
}
