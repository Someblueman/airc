//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

type reactionTransportFixture struct {
	socket     string
	observer   *irc.Client
	account    string
	cred       *detachedCredentials
	authorSpec string
	cert       testtls.Certificate
	http       *http.Client
	httpURL    string
	framed     net.Conn
	ws         *websocket.Conn
	watch      *websocket.Conn
	process    *gatewayProcess
	config     config.Config
}

type reactionReady struct {
	State     string `json:"state"`
	Listeners []struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	} `json:"listeners"`
}

// TestReactionAddHTTPHistoryAndLiveWatch maps reaction.add to a native
// reaction on the parent: both HTTP routes admit durably and coalesce a
// repeated request, the receipt names the native reaction once, and history
// and a live watch both return its reaction facts.
func TestReactionAddHTTPHistoryAndLiveWatch(t *testing.T) {
	f := newReactionTransportFixture(t)
	root := seedReactionParent(t, f)
	startReactionWatch(t, f, root.ID)
	capabilities := core.Request{Version: 1, RequestID: "reaction-capabilities", Operation: core.CapabilitiesGet}
	if status, result := reactionHTTPCall(t, f, f.cred.token(t, capabilities, true)); status != http.StatusOK || result.Capabilities == nil || !hasReactionOperation(result.Capabilities.Operations) {
		t.Fatalf("capabilities omitted reaction.add: status=%d capabilities=%+v", status, result.Capabilities)
	}
	testHeaderReaction(t, f, root)
	for _, reaction := range []string{"checking", "🫶"} {
		r := core.Request{Version: 1, RequestID: "reaction-" + reaction, Operation: core.ReactionAdd, Target: contractRoom(), ParentID: root.ID, Reaction: reaction}
		if reaction != "checking" {
			r.RequestID = "reaction-custom"
		}
		token := f.cred.token(t, r, true)
		for range 2 {
			status, result := reactionHTTPCall(t, f, token)
			if status != http.StatusAccepted || result.Operation != core.ReactionAdd || result.Result == nil || !result.Result.Durable || result.Result.RequestID != r.RequestID {
				t.Fatalf("%s admission did not coalesce: status=%d result=%+v", reaction, status, result)
			}
		}
		result := pollReactionReceipt(t, f, r)
		if result.State != core.Accepted || result.SendAttempts != 1 || result.MessageID == "" || result.Persisted == nil || !*result.Persisted {
			t.Fatalf("%s receipt not accepted exactly once: %+v", reaction, result)
		}
		observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		if observed.From != "fixture-author" || observed.AccountID != f.account || observed.Target != "#gateway-fixture" || observed.Message != reaction || observed.Reaction != reaction || observed.ReplyTo != root.ID || observed.ThreadID != root.ID {
			t.Fatalf("%s native observer lost reaction facts: %+v", reaction, observed)
		}
		live := nextReactionPage(t, f.watch, result.MessageID)
		if live.Reaction != reaction || live.Message != reaction || live.ReplyTo != root.ID || live.ThreadID != root.ID || live.AccountID != f.account {
			t.Fatalf("%s live watch lost reaction facts: %+v", reaction, live)
		}
		history := reactionHistory(t, f, r, result.MessageID)
		if history.ID != result.MessageID || history.Reaction != reaction || history.Message != reaction || history.ReplyTo != root.ID || history.ThreadID != root.ID || history.AccountID != f.account {
			t.Fatalf("%s history lost reaction facts: %+v", reaction, history)
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

func testHeaderReaction(t *testing.T, f *reactionTransportFixture, parent *irc.MessageEvent) {
	t.Helper()
	r := core.Request{Version: 1, RequestID: "reaction-authorSpec", Operation: core.ReactionAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID, Reaction: "seen"}
	body := requestJSON(t, r)
	postStatus, postResult, postLocation := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", body)
	if postStatus != http.StatusAccepted || !postResult.Durable || postLocation != "/v1/requests/"+r.RequestID {
		t.Fatalf("authorSpec reaction POST admission failed: status=%d location=%q result=%+v", postStatus, postLocation, postResult)
	}
	putStatus, putResult, putLocation := headerReactionHTTP(t, f, http.MethodPut, "/v1/requests/"+r.RequestID, body)
	if putStatus != http.StatusAccepted || !putResult.Durable || putLocation != postLocation {
		t.Fatalf("authorSpec reaction PUT coalescing failed: status=%d location=%q result=%+v", putStatus, putLocation, putResult)
	}
	result := pollHeaderReaction(t, f, r.RequestID)
	if result.State != core.Accepted || result.SendAttempts != 1 || result.MessageID == "" || result.Persisted == nil || !*result.Persisted {
		t.Fatalf("authorSpec reaction receipt was not persisted exactly once: %+v", result)
	}
	observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
	if observed.From != "fixture-author" || observed.AccountID != f.account || observed.Target != r.Target.Name || observed.Message != r.Reaction || observed.Reaction != r.Reaction || observed.ReplyTo != parent.ID || observed.ThreadID != parent.ID {
		t.Fatalf("authorSpec reaction native facts mismatch: %+v", observed)
	}
}

func headerReactionHTTP(t *testing.T, f *reactionTransportFixture, method, path string, body []byte) (int, core.Result, string) {
	t.Helper()
	request, err := http.NewRequest(method, f.httpURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, f.authorSpec)
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

func pollHeaderReaction(t *testing.T, f *reactionTransportFixture, requestID string) core.Result {
	t.Helper()
	return acceptedReceipt(t, 8*time.Second, 20*time.Millisecond, "the authorSpec reaction receipt "+requestID+" to become accepted", func() *core.Result {
		request, err := http.NewRequest(http.MethodGet, f.httpURL+"/v1/requests/"+requestID, nil)
		if err != nil {
			t.Fatal(err)
		}
		setAuthorHeaders(request, f.authorSpec)
		response, err := f.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result core.Result
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("authorSpec reaction receipt lookup failed: status=%d err=%v", response.StatusCode, decodeErr)
		}
		return &result
	})
}

const fixtureAccount = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newReactionTransportFixture(t *testing.T) *reactionTransportFixture {
	t.Helper()
	return newReactionTransportFixtureWithAdmin(t, false)
}

func newReactionHTTPFixture(t *testing.T) *reactionTransportFixture {
	t.Helper()
	return newReactionFixture(t, false, nil, true)
}

func newReactionTransportFixtureWithAdmin(t *testing.T, administration bool) *reactionTransportFixture {
	t.Helper()
	return newReactionFixture(t, administration, nil, false)
}

func newReactionFixture(t *testing.T, administration bool, targets []core.Target, httpOnly bool) *reactionTransportFixture {
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
	cred := provisionDetached(t, &c)
	authorSpec := fixtureAuthor + ":" + fixtureAccount
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Limits.Watches, c.Limits.WatchesPerAuthor = 4, 2
	cert := testtls.New(t)
	c.Listeners = []config.Listener{
		{ID: "https", Protocol: "http", Enabled: true, TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: dispatch.ImplementedOperations()},
	}
	if !httpOnly {
		c.Listeners = append(c.Listeners,
			config.Listener{ID: "framed", Protocol: "framed", Enabled: true, Operations: dispatch.ImplementedOperations()},
			config.Listener{ID: "websocket", Protocol: "websocket", Enabled: true, Operations: dispatch.ImplementedOperations()},
		)
	}
	// Hold every reservation until all listener addresses have been chosen.
	reservations := make([]net.Listener, 0, len(c.Listeners))
	for i := range c.Listeners {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		reservations = append(reservations, listener)
		c.Listeners[i].Address = listener.Addr().String()
	}
	for _, listener := range reservations {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// The listeners admit the fixture room and the author's own direct
	// target, plus any extra targets; others are refused before admission.
	allowed := []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	for _, target := range targets {
		if !slices.Contains(allowed, target) {
			allowed = append(allowed, target)
		}
	}
	for i := range c.Listeners {
		c.Listeners[i].Targets = allowed
		// Quota tests send many requests as one author within a minute.
		c.Listeners[i].Limits = &config.ListenerLimits{RequestsPerMinute: 10000, AuthorRequestsPerMinute: 10000}
	}
	p := startGateway(t, gatewayBinary(t), c)
	ready := reactionGatewayReady(t, p, len(c.Listeners))
	transport := &http.Transport{TLSClientConfig: cert.Client.Clone()}
	t.Cleanup(transport.CloseIdleConnections)
	f := &reactionTransportFixture{
		socket: socket, observer: observer, cred: cred, authorSpec: authorSpec, cert: cert, http: &http.Client{Transport: transport, Timeout: 8 * time.Second},
		httpURL: "https://" + ready["https"], process: p, config: c,
	}
	if httpOnly {
		return f
	}
	var err error
	f.framed, err = net.DialTimeout("tcp", ready["framed"], 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.framed.Close() })
	f.ws = reactionWebSocketDial(t, "ws://"+ready["websocket"]+"/v1/socket")
	t.Cleanup(func() { _ = f.ws.CloseNow() })
	f.watch = reactionWebSocketDial(t, "ws://"+ready["websocket"]+"/v1/socket")
	t.Cleanup(func() { _ = f.watch.CloseNow() })
	return f
}

// restart stops the gateway with SIGINT and starts it again on the same
// journal and listener addresses. Only the HTTP listener is reconnected.
func (f *reactionTransportFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal(f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("gateway did not stop for restart")
	}
	f.http.Transport.(*http.Transport).CloseIdleConnections()
	f.process = startGateway(t, gatewayBinary(t), f.config)
	if ready := reactionGatewayReady(t, f.process, len(f.config.Listeners)); "https://"+ready["https"] != f.httpURL {
		t.Fatalf("restarted gateway moved its HTTP listener: %v", ready)
	}
}

func reactionGatewayReady(t *testing.T, p *gatewayProcess, listeners int) map[string]string {
	t.Helper()
	select {
	case data := <-p.ready:
		var ready reactionReady
		if err := json.Unmarshal(data, &ready); err != nil || ready.State != "ready" || len(ready.Listeners) != listeners {
			t.Fatalf("invalid fixture ready record: %s (%v)", data, err)
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
	websocketWrite(t, f.watch, frameRequest{Version: 1, CorrelationID: "reaction-watch", Profile: "plain", Request: token, Stream: true})
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
	request, err := http.NewRequest(http.MethodPost, f.httpURL+"/v1/request", strings.NewReader(token))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
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

func pollReactionReceipt(t *testing.T, f *reactionTransportFixture, r core.Request) core.Result {
	t.Helper()
	return acceptedReceipt(t, 8*time.Second, 20*time.Millisecond, "the reaction receipt "+r.RequestID+" to become accepted", func() *core.Result {
		statusRequest := core.Request{Version: 1, RequestID: "receipt-" + r.RequestID, Operation: core.ReceiptGet, LookupID: r.RequestID}
		status, result := reactionHTTPCall(t, f, f.cred.token(t, statusRequest, true))
		if status != http.StatusOK {
			return nil
		}
		return result.Result
	})
}

// reactionHistory reads the request target's recent history over HTTP and
// returns message id from it.
func reactionHistory(t *testing.T, f *reactionTransportFixture, reaction core.Request, id string) *irc.MessageEvent {
	t.Helper()
	r := core.Request{Version: 1, RequestID: "history-" + reaction.RequestID, Operation: core.HistoryRead, Target: reaction.Target, Limit: 50, Start: "recent"}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
	if status != http.StatusOK || result.Page == nil {
		t.Fatalf("history failed: status=%d result=%+v", status, result)
	}
	for _, message := range result.Page.Messages {
		if message.ID == id {
			return &irc.MessageEvent{ChatMetadata: message.ChatMetadata, ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}
		}
	}
	t.Fatalf("message missing from history: %s", id)
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
