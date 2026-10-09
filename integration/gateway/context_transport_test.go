//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

type contextWire struct {
	Summary      irc.ContextSummary    `json:"summary"`
	Messages     []irc.MessageMetadata `json:"messages"`
	Pins         []irc.MessageMetadata `json:"pins"`
	Participants []irc.AgentCard       `json:"participants"`
}

func TestContextReadAcrossEncryptedTransportsAndBearer(t *testing.T) {
	f := newReactionTransportFixture(t)

	root, reply, reaction := seedContextConversation(t, f)
	correction := seedContextCorrection(t, f, root.ID)
	seedContextPinProfile(t, f, root.ID)
	before := contextCheckSnapshot(t, f)
	for _, profile := range []struct {
		name      string
		encrypted bool
	}{{name: "encrypted", encrypted: true}, {name: "signed"}} {
		request := core.Request{Version: 1, RequestID: "context-all-transports-" + profile.name, Operation: core.ContextRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: reply.ID, Limit: 50, MaxBytes: 64 << 10}
		transports := []struct {
			name string
			call func(string) (int, dispatch.DetachedResult)
		}{
			{name: "http", call: func(token string) (int, dispatch.DetachedResult) {
				return contextHTTPCall(t, f, token, profile.encrypted)
			}},
			{name: "framed", call: func(token string) (int, dispatch.DetachedResult) {
				return contextFramedCall(t, f.framed, token, "context-framed-"+profile.name, profile.encrypted)
			}},
			{name: "websocket", call: func(token string) (int, dispatch.DetachedResult) {
				return contextWebSocketCall(t, f.ws, token, "context-websocket-"+profile.name, profile.encrypted)
			}},
		}
		if !profile.encrypted {
			transports = transports[:1] // plaintext framed/WebSocket listeners are encrypted-only.
		}
		for _, transport := range transports {
			status, result := transport.call(f.cred.token(t, request, profile.encrypted))
			if status != http.StatusOK || result.Operation != core.ContextRead || result.Context == nil {
				t.Fatalf("%s %s context response failed: status=%d result=%+v", profile.name, transport.name, status, result)
			}
			assertContextFacts(t, result.Context, root, reply, reaction)
			if !containsCorrection(marshalContext(t, result.Context).Messages, correction.ID, root.ID) {
				t.Fatalf("%s %s context lost correction metadata", profile.name, transport.name)
			}
		}
	}

	query := "/v1/context?kind=room&target=" + url.QueryEscape("#gateway-fixture") + "&parent_id=" + url.QueryEscape(reply.ID)
	status, body := bearerContext(t, f, query)
	if status != http.StatusOK {
		t.Fatalf("bearer context failed: status=%d body=%s", status, body)
	}
	var snapshot contextWire
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal("decode bearer context:", err)
	}
	assertContextWire(t, snapshot, root, reply, reaction)
	h2 := *f
	h2transport := f.http.Transport.(*http.Transport).Clone()
	h2transport.ForceAttemptHTTP2 = true
	t.Cleanup(h2transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: h2transport, Timeout: f.http.Timeout}
	status, body = bearerContext(t, &h2, query)
	if status != http.StatusOK || json.Unmarshal(body, &snapshot) != nil {
		t.Fatalf("HTTP/2 context failed: status=%d body=%s", status, body)
	}
	assertContextWire(t, snapshot, root, reply, reaction)
	h2request := core.Request{Version: 1, RequestID: "context-signed-http2", Operation: core.ContextRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: reply.ID}
	status, detached := contextHTTPCall(t, &h2, f.cred.token(t, h2request, false), false)
	if status != http.StatusOK || detached.Context == nil {
		t.Fatalf("signed HTTP/2 context failed: status=%d result=%+v", status, detached)
	}
	assertContextFacts(t, detached.Context, root, reply, reaction)
	if len(snapshot.Pins) == 0 || snapshot.Pins[0].ID != root.ID || len(snapshot.Participants) == 0 || snapshot.Participants[0].Model != "context-model" {
		t.Fatalf("bearer context lost pin/profile: pins=%+v participants=%+v", snapshot.Pins, snapshot.Participants)
	}
	if status, _ := bearerContext(t, f, "/v1/requests/context-all-transports-encrypted"); status != http.StatusNotFound {
		t.Fatalf("context read created a delivery receipt: status=%d", status)
	}
	for _, bad := range []string{
		query + "&parent_id=" + url.QueryEscape(reply.ID),
		query + "&cursor=x", query + "&start=recent", query + "&from=fixture-author", query + "&unknown=x",
		query + "&limit=-1", query + "&limit=1001", query + "&max_bytes=512", query + "&max_bytes=65537",
	} {
		if status, _ := bearerContext(t, f, bad); status != http.StatusBadRequest {
			t.Fatalf("invalid bearer context query accepted: %s status=%d", bad, status)
		}
	}
	if status, _ := bearerContextWithAuth(t, f, query, "not-a-bearer"); status != http.StatusUnauthorized {
		t.Fatalf("invalid bearer context credential status=%d", status)
	}
	if after := contextCheckSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("context reads mutated native CHECK snapshot: before=%v after=%v", before, after)
	}
}

func TestContextReadDeniesWrongRoomMissingAndDMWithoutMutation(t *testing.T) {
	f := newReactionTransportFixture(t)

	root, _, _ := seedContextConversation(t, f)
	foreign := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	if err := foreign.SendWithID("#other-private", "foreign context trigger", "context-foreign"); err != nil {
		t.Fatal(err)
	}
	foreignReceipt := next(t, foreign, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "context-foreign" })
	_ = foreign.Close()

	cases := []struct {
		name   string
		parent string
		target *core.Target
		want   int
	}{
		{name: "wrong-room", parent: foreignReceipt.ID, target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, want: http.StatusForbidden},
		{name: "missing-trigger", parent: strings.Repeat("a", 32), target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, want: http.StatusForbidden},
		{name: "direct-target", parent: root.ID, target: &core.Target{Kind: "direct", Name: "fixture-author"}, want: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		r := core.Request{Version: 1, RequestID: "context-deny-" + tc.name, Operation: core.ContextRead, Target: tc.target, ParentID: tc.parent, Limit: 10}
		status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
		if status != tc.want || result.Context != nil {
			t.Fatalf("%s disclosure/status mismatch: status=%d want=%d result=%+v", tc.name, status, tc.want, result)
		}
	}
}

func TestContextReadReportsRecordAndByteOmissions(t *testing.T) {
	f := newReactionTransportFixture(t)

	root, _, _ := seedContextConversation(t, f)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	for i := 0; i < 24; i++ {
		id := "context-filler-" + string(rune('a'+i))
		if err := author.ReplyWithID(root.ID, strings.Repeat("context filler "+id+" 🙂 ", 100), id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	for _, tc := range []struct {
		name     string
		limit    int
		maxBytes int
	}{
		{name: "records", limit: 1, maxBytes: 64 << 10},
		{name: "bytes", limit: 50, maxBytes: 1024},
	} {
		r := core.Request{Version: 1, RequestID: "context-omit-" + tc.name, Operation: core.ContextRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: root.ID, Limit: tc.limit, MaxBytes: tc.maxBytes}
		status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
		if status != http.StatusOK || result.Context == nil {
			t.Fatalf("%s bounded context failed: status=%d result=%+v", tc.name, status, result)
		}
		wire := marshalContext(t, result.Context)
		if wire.Summary.OmittedMessages+wire.Summary.OmittedPins+wire.Summary.OmittedProfiles == 0 {
			t.Fatalf("%s context did not report omissions: %+v", tc.name, wire.Summary)
		}
		for _, message := range wire.Messages {
			if !strings.Contains(message.Message, "context") && message.ID != root.ID {
				t.Fatalf("%s returned a truncated/unexpected body: %+v", tc.name, message)
			}
		}
	}
}

// A retained reply remains addressable after the root falls out of the native
// FIFO; the context must report the missing root rather than inventing one.
func TestContextReadRetainedReplyReportsEvictedRoot(t *testing.T) {
	f := newReactionTransportFixture(t)

	root, reply, _ := seedContextConversation(t, f)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	for i := 0; i < 30; i++ {
		id := "context-evict-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if err := author.SendWithID("#gateway-fixture", "evicting body "+id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	r := core.Request{Version: 1, RequestID: "context-evicted-root", Operation: core.ContextRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: reply.ID, Limit: 50}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
	if status != http.StatusOK || result.Context == nil {
		t.Fatalf("retained reply context failed: status=%d result=%+v", status, result)
	}
	wire := marshalContext(t, result.Context)
	if !containsString(wire.Summary.Missing, root.ID) {
		t.Fatalf("context omitted evicted root without reporting it: %+v", wire.Summary)
	}
	if !containsMessage(wire.Messages, reply.ID) {
		t.Fatalf("context lost retained reply: %+v", wire.Messages)
	}
}

func seedContextConversation(t *testing.T, f *reactionTransportFixture) (*irc.MessageEvent, *irc.MessageEvent, *irc.MessageEvent) {
	t.Helper()
	root := seedReactionParent(t, f)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.ReplyWithID(root.ID, "context reply\nwhole body 🙂", "context-reply"); err != nil {
		t.Fatal(err)
	}
	replyReceipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "context-reply" })
	reply := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == replyReceipt.ID })
	if err := author.React(root.ID, "agree"); err != nil {
		t.Fatal(err)
	}
	reactionReceipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Reaction == "agree" && e.ReplyTo == root.ID })
	reaction := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == reactionReceipt.ID })
	return root, reply, reaction
}

func seedContextPinProfile(t *testing.T, f *reactionTransportFixture, id string) {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "pin", ID: id}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "pin" && e.ID == id })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := author.UpdateProfile(map[string]string{"model": "context-model", "workspace": "context-workspace", "tools": "context-tools", "about": "context profile 🙂"}); err != nil {
		t.Fatal(err)
	}
	if err := author.Directory("fixture-author"); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.DirectoryEvent) bool { return e.Nick == "fixture-author" && e.Model == "context-model" })
	next(t, author, func(e *irc.EndOfDirectoryEvent) bool { return true })
}

func seedContextCorrection(t *testing.T, f *reactionTransportFixture, id string) *irc.MessageEvent {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "correct", ID: id, Text: "corrected context root 🙂"}); err != nil {
		t.Fatal(err)
	}
	entry := next(t, author, func(e *irc.ChatEvent) bool {
		return e.Action == "correct" && e.Message != nil && e.Message.Supersedes == id
	})
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	return &irc.MessageEvent{ChatMetadata: entry.Message.ChatMetadata, ID: entry.Message.ID, ReplyTo: entry.Message.ReplyTo, ThreadID: entry.Message.ThreadID, Reaction: entry.Message.Reaction, From: entry.Message.From, Target: entry.Message.Target, Message: entry.Message.Message, Timestamp: entry.Message.Timestamp}
}

func bearerContext(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	return bearerContextWithAuth(t, f, path, f.bearer)
}

func bearerContextWithAuth(t *testing.T, f *reactionTransportFixture, path, token string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, f.httpURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertContextHTTPProtocol(t, f, response)
	var body []byte
	body, err = io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func contextHTTPCall(t *testing.T, f *reactionTransportFixture, token string, encrypted bool) (int, dispatch.DetachedResult) {
	path := "/v1/envelopes/signed"
	if encrypted {
		path = "/v1/envelopes/encrypted"
	}
	request, err := http.NewRequest(http.MethodPost, f.httpURL+path, strings.NewReader(token))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/jose")
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertContextHTTPProtocol(t, f, response)
	var result dispatch.DetachedResult
	if err := json.NewDecoder(io.LimitReader(response.Body, (64<<10)+1)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, result
}

func assertContextHTTPProtocol(t *testing.T, f *reactionTransportFixture, response *http.Response) {
	t.Helper()
	want := 1
	if f.http.Transport.(*http.Transport).ForceAttemptHTTP2 {
		want = 2
	}
	if response.ProtoMajor != want {
		t.Fatalf("context HTTP protocol = %s, want major %d", response.Proto, want)
	}
}

func contextFramedCall(t *testing.T, conn net.Conn, token, id string, encrypted bool) (int, dispatch.DetachedResult) {
	profile := "signed"
	if encrypted {
		profile = "encrypted"
	}
	writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: id, Profile: profile, Envelope: token})
	response := readFrame(t, conn)
	if response.Result == nil {
		t.Logf("framed context error: %+v", response)
		return http.StatusBadRequest, dispatch.DetachedResult{}
	}
	return http.StatusOK, *response.Result
}

func contextWebSocketCall(t *testing.T, conn *websocket.Conn, token, id string, encrypted bool) (int, dispatch.DetachedResult) {
	profile := "signed"
	if encrypted {
		profile = "encrypted"
	}
	websocketWrite(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: id, Profile: profile, Envelope: token})
	response := websocketRead(t, conn)
	if response.Result == nil {
		t.Logf("websocket context error: %+v", response)
		return http.StatusBadRequest, dispatch.DetachedResult{}
	}
	return http.StatusOK, *response.Result
}

func contextCheckSnapshot(t *testing.T, f *reactionTransportFixture) map[string]string {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	entries, err := irc.RequestCheck(context.Background(), author, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#gateway-fixture", Limit: 64}}, IncludeOwn: true, MaxMessages: 128}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make(map[string]string)
	for _, entry := range entries {
		if entry.Message != nil {
			snapshot[entry.Message.ID] = entry.Message.Message
		}
	}
	return snapshot
}

func marshalContext(t *testing.T, value any) contextWire {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var wire contextWire
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}

func assertContextFacts(t *testing.T, value any, root, reply, reaction *irc.MessageEvent) {
	t.Helper()
	assertContextWire(t, marshalContext(t, value), root, reply, reaction)
}

func assertContextWire(t *testing.T, wire contextWire, root, reply, reaction *irc.MessageEvent) {
	t.Helper()
	if wire.Summary.TriggerID != reply.ID || wire.Summary.RootID != root.ID || !containsMessage(wire.Messages, root.ID) || !containsMessage(wire.Messages, reply.ID) || !containsMessage(wire.Messages, reaction.ID) {
		t.Fatalf("context lost trigger/thread/reaction facts: summary=%+v messages=%+v", wire.Summary, wire.Messages)
	}
	if len(wire.Pins) == 0 || wire.Pins[0].ID != root.ID || len(wire.Participants) == 0 || wire.Participants[0].Model != "context-model" {
		t.Fatalf("context lost pin/profile: pins=%+v participants=%+v", wire.Pins, wire.Participants)
	}
	for _, message := range wire.Messages {
		if message.ID == root.ID && message.Message != root.Message || message.ID == reply.ID && (message.Message != reply.Message || message.ReplyTo != root.ID || message.ThreadID != root.ID) || message.ID == reaction.ID && (message.Reaction != "agree" || message.Message != "agree" || message.ReplyTo != root.ID || message.ThreadID != root.ID) {
			t.Fatalf("context metadata/body mismatch: %+v", message)
		}
	}
}

func containsMessage(messages []irc.MessageMetadata, id string) bool {
	for _, message := range messages {
		if message.ID == id {
			return true
		}
	}
	return false
}

func containsCorrection(messages []irc.MessageMetadata, id, supersedes string) bool {
	for _, message := range messages {
		if message.ID == id && message.Supersedes == supersedes && message.Message == "corrected context root 🙂" {
			return true
		}
	}
	return false
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
