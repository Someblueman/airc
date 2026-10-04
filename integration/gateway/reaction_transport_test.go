//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

type reactionTransportFixture struct {
	socket   string
	observer *irc.Client
	account  string
	cred     *detachedCredentials
	bearer   string
	cert     testtls.Certificate
	http     *http.Client
	httpURL  string
	framed   net.Conn
	ws       *websocket.Conn
	watch    *websocket.Conn
}

type reactionReady struct {
	State     string `json:"state"`
	Listeners []struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	} `json:"listeners"`
}

func TestReactionAddAcrossHTTPFramedAndWebSocket(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()

	root := seedReactionParent(t, f)
	checkReactionCapabilities(t, f)
	testBearerReaction(t, f, root)
	startReactionWatch(t, f, root.ID)

	cases := []struct {
		name     string
		reaction string
		request  core.Request
	}{
		{name: "http", reaction: "checking"},
		{name: "framed", reaction: "🫶"},
		{name: "websocket", reaction: "✨"},
	}
	for i := range cases {
		cases[i].request = core.Request{
			Version: 1, RequestID: "reaction-" + cases[i].name, Operation: core.ReactionAdd,
			Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: root.ID, Reaction: cases[i].reaction,
		}
		token := f.cred.token(t, cases[i].request, true)
		admissions := []struct {
			name string
			call func(string) (int, dispatch.DetachedResult)
		}{
			{name: "http", call: func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
			{name: "framed", call: func(token string) (int, dispatch.DetachedResult) {
				return reactionFramedCall(t, f.framed, token, cases[i].name+"-admit")
			}},
			{name: "websocket", call: func(token string) (int, dispatch.DetachedResult) {
				return reactionWebSocketCall(t, f.ws, token, cases[i].name+"-admit")
			}},
		}
		for _, admission := range admissions {
			status, result := admission.call(token)
			if status != http.StatusAccepted || result.Operation != core.ReactionAdd || result.Result == nil || !result.Result.Durable || result.Result.RequestID != cases[i].request.RequestID {
				t.Fatalf("%s %s admission did not coalesce encrypted reaction: status=%d result=%+v", cases[i].name, admission.name, status, result)
			}
		}

		ids := make(map[string]string, len(admissions))
		for _, admission := range admissions {
			result := pollReactionReceipt(t, f, cases[i].request, admission.name)
			if result.State != core.Accepted || result.SendAttempts != 1 || result.MessageID == "" || result.Persisted == nil || !*result.Persisted {
				t.Fatalf("%s %s receipt not accepted exactly once: %+v", cases[i].name, admission.name, result)
			}
			ids[admission.name] = result.MessageID
		}
		if ids["http"] != ids["framed"] || ids["http"] != ids["websocket"] {
			t.Fatalf("%s repeated encrypted envelope changed native ID: %v", cases[i].name, ids)
		}
		observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == ids["http"] })
		if observed.From != "fixture-author" || observed.AccountID != f.account || observed.Target != "#gateway-fixture" || observed.Message != cases[i].reaction || observed.Reaction != cases[i].reaction || observed.ReplyTo != root.ID || observed.ThreadID != root.ID {
			t.Fatalf("%s native observer lost reaction facts: %+v", cases[i].name, observed)
		}
		live := nextReactionPage(t, f.watch, ids["http"])
		if live.ID != ids["http"] || live.Reaction != cases[i].reaction || live.Message != cases[i].reaction || live.ReplyTo != root.ID || live.ThreadID != root.ID || live.AccountID != f.account {
			t.Fatalf("%s live watch lost reaction facts: %+v", cases[i].name, live)
		}

		history := reactionHistory(t, f, cases[i].request, cases[i].name, ids["http"])
		if history.ID != ids["http"] || history.Reaction != cases[i].reaction || history.Message != cases[i].reaction || history.ReplyTo != root.ID || history.ThreadID != root.ID || history.AccountID != f.account {
			t.Fatalf("%s history lost reaction facts: %+v", cases[i].name, history)
		}
	}

	for _, test := range []struct {
		name   string
		mutate func(*core.Request)
		status int
	}{
		{name: "malformed reaction", mutate: func(r *core.Request) { r.Reaction = "bad reaction" }, status: http.StatusUnauthorized},
		{name: "wrong room", mutate: func(r *core.Request) { r.Target = &core.Target{Kind: "room", Name: "#other-private"} }, status: http.StatusForbidden},
		{name: "direct target", mutate: func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-author"} }, status: http.StatusUnauthorized},
		{name: "malformed parent", mutate: func(r *core.Request) { r.ParentID = "not-a-message-id" }, status: http.StatusUnauthorized},
	} {
		requestID := "reaction-invalid-" + strings.ReplaceAll(test.name, " ", "-")
		r := core.Request{Version: 1, RequestID: requestID, Operation: core.ReactionAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: root.ID, Reaction: "agree"}
		test.mutate(&r)
		status, _ := reactionHTTPCall(t, f, f.cred.token(t, r, true))
		if status != test.status {
			t.Fatalf("%s prewrite status=%d, want %d", test.name, status, test.status)
		}
	}
}

func checkReactionCapabilities(t *testing.T, f *reactionTransportFixture) {
	t.Helper()
	transports := []struct {
		name string
		call func(string) (int, dispatch.DetachedResult)
	}{
		{name: "http", call: func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{name: "framed", call: func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "capabilities-framed")
		}},
		{name: "websocket", call: func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "capabilities-websocket")
		}},
	}
	for _, transport := range transports {
		r := core.Request{Version: 1, RequestID: "reaction-capabilities-" + transport.name, Operation: core.CapabilitiesGet}
		status, result := transport.call(f.cred.token(t, r, true))
		if status != http.StatusOK || result.Capabilities == nil || !hasReactionOperation(result.Capabilities.Operations) {
			t.Fatalf("%s capabilities omitted reaction.add: status=%d capabilities=%+v", transport.name, status, result.Capabilities)
		}
	}
}

func hasReactionOperation(operations []core.Operation) bool {
	for _, operation := range operations {
		if operation == core.ReactionAdd {
			return true
		}
	}
	return false
}

func testBearerReaction(t *testing.T, f *reactionTransportFixture, parent *irc.MessageEvent) {
	t.Helper()
	r := core.Request{Version: 1, RequestID: "reaction-bearer", Operation: core.ReactionAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID, Reaction: "seen"}
	body := requestJSON(t, r)
	postStatus, postResult, postLocation := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", body)
	if postStatus != http.StatusAccepted || !postResult.Durable || postLocation != "/v1/requests/"+r.RequestID {
		t.Fatalf("bearer reaction POST admission failed: status=%d location=%q result=%+v", postStatus, postLocation, postResult)
	}
	putStatus, putResult, putLocation := bearerReactionHTTP(t, f, http.MethodPut, "/v1/requests/"+r.RequestID, body)
	if putStatus != http.StatusAccepted || !putResult.Durable || putLocation != postLocation {
		t.Fatalf("bearer reaction PUT coalescing failed: status=%d location=%q result=%+v", putStatus, putLocation, putResult)
	}
	result := pollBearerReaction(t, f, r.RequestID)
	if result.State != core.Accepted || result.SendAttempts != 1 || result.MessageID == "" || result.Persisted == nil || !*result.Persisted {
		t.Fatalf("bearer reaction receipt was not persisted exactly once: %+v", result)
	}
	observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
	if observed.From != "fixture-author" || observed.AccountID != f.account || observed.Target != r.Target.Name || observed.Message != r.Reaction || observed.Reaction != r.Reaction || observed.ReplyTo != parent.ID || observed.ThreadID != parent.ID {
		t.Fatalf("bearer reaction native facts mismatch: %+v", observed)
	}
}

func bearerReactionHTTP(t *testing.T, f *reactionTransportFixture, method, path string, body []byte) (int, core.Result, string) {
	t.Helper()
	request, err := http.NewRequest(method, f.httpURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.bearer)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result core.Result
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, result, response.Header.Get("Location")
}

func pollBearerReaction(t *testing.T, f *reactionTransportFixture, requestID string) core.Result {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, f.httpURL+"/v1/requests/"+requestID, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+f.bearer)
		response, err := f.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result core.Result
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("bearer reaction receipt lookup failed: status=%d err=%v", response.StatusCode, decodeErr)
		}
		if result.State == core.Accepted {
			return result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("bearer reaction receipt did not become accepted", requestID)
	return core.Result{}
}

const fixtureAccount = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newReactionTransportFixture(t *testing.T) *reactionTransportFixture {
	t.Helper()
	return newReactionTransportFixtureWithAdmin(t, false)
}

func newReactionTransportFixtureWithAdmin(t *testing.T, administration bool) *reactionTransportFixture {
	return newReactionTransportFixtureWithTargets(t, administration, nil)
}

func newReactionTransportFixtureWithTargets(t *testing.T, administration bool, targets []core.Target) *reactionTransportFixture {
	t.Helper()
	socket := fixtureWithAdmin(t, administration)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	c.Principals[0].Targets = append(c.Principals[0].Targets, targets...)
	cred := provisionDetached(t, &c)
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	bearer, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 4, 2
	cert := testtls.New(t)
	c.Listeners = []config.Listener{
		{ID: "https", Protocol: "http", Enabled: true, Address: reactionFreeAddress(t), TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: dispatch.ImplementedOperations()},
		{ID: "framed", Protocol: "framed", Enabled: true, Address: reactionFreeAddress(t), Operations: dispatch.ImplementedOperations()},
		{ID: "websocket", Protocol: "websocket", Enabled: true, Address: reactionFreeAddress(t), Operations: dispatch.ImplementedOperations()},
	}
	p := startGateway(t, gatewayBinary(t), c)
	ready := reactionGatewayReady(t, p)
	transport := &http.Transport{TLSClientConfig: cert.Client.Clone()}
	t.Cleanup(transport.CloseIdleConnections)
	f := &reactionTransportFixture{
		socket: socket, observer: observer, cred: cred, bearer: bearer, cert: cert, http: &http.Client{Transport: transport, Timeout: 8 * time.Second},
		httpURL: "https://" + ready["https"],
	}
	f.framed, err = net.DialTimeout("tcp", ready["framed"], 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f.ws = reactionWebSocketDial(t, "ws://"+ready["websocket"]+"/v1/socket")
	f.watch = reactionWebSocketDial(t, "ws://"+ready["websocket"]+"/v1/socket")
	return f
}

func reactionFreeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func reactionGatewayReady(t *testing.T, p *gatewayProcess) map[string]string {
	t.Helper()
	select {
	case data := <-p.ready:
		var ready reactionReady
		if err := json.Unmarshal(data, &ready); err != nil || ready.State != "ready" || len(ready.Listeners) != 3 {
			t.Fatalf("invalid multi-transport ready record: %s (%v)", data, err)
		}
		addresses := make(map[string]string, len(ready.Listeners))
		for _, listener := range ready.Listeners {
			addresses[listener.ID] = listener.Address
		}
		return addresses
	case <-p.done:
		t.Fatal("gateway exited before multi-transport ready", p.err, p.stderr.String())
	case <-time.After(8 * time.Second):
		t.Fatal("gateway failed to become ready")
	}
	return nil
}

func seedReactionParent(t *testing.T, f *reactionTransportFixture) *irc.MessageEvent {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	const requestID = "reaction-seed"
	if err := author.SendWithID("#gateway-fixture", "native reaction parent", requestID); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == requestID })
	parent := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	if parent.From != "fixture-author" || parent.Target != "#gateway-fixture" || parent.Message != "native reaction parent" {
		t.Fatalf("native seed mismatch: %+v", parent)
	}
	f.account = parent.AccountID
	return parent
}

func startReactionWatch(t *testing.T, f *reactionTransportFixture, parentID string) {
	t.Helper()
	r := core.Request{Version: 1, RequestID: "reaction-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 50, Start: "recent"}
	token := f.cred.token(t, r, true)
	websocketWrite(t, f.watch, adapter.FrameRequest{Version: 1, CorrelationID: "reaction-watch", Profile: "encrypted", Envelope: token, Stream: true})
	first := websocketRead(t, f.watch)
	if first.Event != "page" || first.Page == nil {
		t.Fatalf("reaction watch did not return initial page for parent %s: %+v", parentID, first)
	}
}

func nextReactionPage(t *testing.T, conn *websocket.Conn, id string) *irc.MessageEvent {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		response := websocketRead(t, conn)
		if response.Event == "end" {
			t.Fatalf("reaction watch ended before %s: %+v", id, response)
		}
		if response.Page == nil {
			continue
		}
		for _, message := range response.Page.Messages {
			if message.ID == id {
				return &irc.MessageEvent{ChatMetadata: message.ChatMetadata, ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}
			}
		}
	}
	t.Fatalf("reaction watch did not observe %s", id)
	return nil
}

func reactionHTTPCall(t *testing.T, f *reactionTransportFixture, token string) (int, dispatch.DetachedResult) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, f.httpURL+"/v1/envelopes/encrypted", strings.NewReader(token))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/jose")
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result dispatch.DetachedResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, result
}

func reactionFramedCall(t *testing.T, conn net.Conn, token, id string) (int, dispatch.DetachedResult) {
	t.Helper()
	writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: id, Profile: "encrypted", Envelope: token})
	response := readFrame(t, conn)
	if response.Event == "error" {
		return http.StatusBadRequest, dispatch.DetachedResult{}
	}
	if response.Result == nil {
		t.Fatalf("framed reaction response missing result: %+v", response)
	}
	return resultStatus(response.Result), *response.Result
}

func reactionWebSocketCall(t *testing.T, conn *websocket.Conn, token, id string) (int, dispatch.DetachedResult) {
	t.Helper()
	websocketWrite(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: id, Profile: "encrypted", Envelope: token})
	response := websocketRead(t, conn)
	if response.Event == "error" {
		return http.StatusBadRequest, dispatch.DetachedResult{}
	}
	if response.Result == nil {
		t.Fatalf("WebSocket reaction response missing result: %+v", response)
	}
	return resultStatus(response.Result), *response.Result
}

func resultStatus(result *dispatch.DetachedResult) int {
	if result.Result != nil && result.Operation == core.ReactionAdd {
		return http.StatusAccepted
	}
	return http.StatusOK
}

func pollReactionReceipt(t *testing.T, f *reactionTransportFixture, r core.Request, transport string) core.Result {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		statusRequest := core.Request{Version: 1, RequestID: "receipt-" + transport + "-" + r.RequestID, Operation: core.ReceiptGet, LookupID: r.RequestID}
		token := f.cred.token(t, statusRequest, true)
		var result dispatch.DetachedResult
		var status int
		switch transport {
		case "http":
			status, result = reactionHTTPCall(t, f, token)
		case "framed":
			status, result = reactionFramedCall(t, f.framed, token, "receipt-"+r.RequestID)
		case "websocket":
			status, result = reactionWebSocketCall(t, f.ws, token, "receipt-"+r.RequestID)
		}
		if status == http.StatusOK && result.Result != nil && result.Result.State == core.Accepted {
			return *result.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("reaction receipt did not become accepted", r.RequestID, transport)
	return core.Result{}
}

func reactionHistory(t *testing.T, f *reactionTransportFixture, reaction core.Request, transport, id string) *irc.MessageEvent {
	t.Helper()
	r := core.Request{Version: 1, RequestID: "history-" + transport + "-" + reaction.RequestID, Operation: core.HistoryRead, Target: reaction.Target, Limit: 50, Start: "recent"}
	token := f.cred.token(t, r, true)
	var result dispatch.DetachedResult
	var status int
	switch transport {
	case "http":
		status, result = reactionHTTPCall(t, f, token)
	case "framed":
		status, result = reactionFramedCall(t, f.framed, token, "history-"+reaction.RequestID)
	case "websocket":
		status, result = reactionWebSocketCall(t, f.ws, token, "history-"+reaction.RequestID)
	}
	if status != http.StatusOK || result.Page == nil {
		t.Fatalf("%s reaction history failed: status=%d result=%+v", transport, status, result)
	}
	for _, message := range result.Page.Messages {
		if message.ID == id {
			return &irc.MessageEvent{ChatMetadata: message.ChatMetadata, ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}
		}
	}
	t.Fatalf("%s reaction missing from history: %s", transport, id)
	return nil
}

func reactionWebSocketDial(t *testing.T, address string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{Subprotocols: []string{adapter.WebSocketSubprotocol}})
	if err != nil {
		if response != nil {
			t.Fatalf("reaction WebSocket upgrade: %s: %v", response.Status, err)
		}
		t.Fatal(err)
	}
	conn.SetReadLimit(adapter.MaxResponseFrameBytes)
	return conn
}
