//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

type contextWire struct {
	Summary      irc.ContextSummary    `json:"summary"`
	Messages     []irc.MessageMetadata `json:"messages"`
	Pins         []irc.MessageMetadata `json:"pins"`
	Participants []irc.AgentCard       `json:"participants"`
}

// TestContextReadThreadFactsPinsAndProfiles maps context.read to the native
// conversation around a trigger: the root, reply, reaction and correction with
// their metadata, the room's pins and the participants' profiles, identical
// through both HTTP routes.
func TestContextReadThreadFactsPinsAndProfiles(t *testing.T) {
	f := newReactionHTTPFixture(t)
	root, reply, reaction := seedContextConversation(t, f)
	correction := seedContextCorrection(t, f, root.ID)
	seedContextPinProfile(t, f, root.ID)
	request := core.Request{Version: 1, RequestID: "context-read", Operation: core.ContextRead, Target: contractRoom(), ParentID: reply.ID, Limit: 50, MaxBytes: 64 << 10}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusOK || result.Operation != core.ContextRead || result.Context == nil {
		t.Fatalf("context response failed: status=%d result=%+v", status, result)
	}
	assertContextFacts(t, result.Context, root, reply, reaction)
	if !containsCorrection(marshalContext(t, result.Context).Messages, correction.ID, root.ID) {
		t.Fatal("context lost correction metadata")
	}
	status, body := headerContext(t, f, "/v1/context?kind=room&target="+url.QueryEscape("#gateway-fixture")+"&parent_id="+url.QueryEscape(reply.ID))
	var snapshot contextWire
	if status != http.StatusOK || json.Unmarshal(body, &snapshot) != nil {
		t.Fatalf("GET context failed: status=%d body=%s", status, body)
	}
	assertContextWire(t, snapshot, root, reply, reaction)
}

func TestContextReadDeniesWrongRoomMissingAndDMWithoutMutation(t *testing.T) {
	f := newReactionHTTPFixture(t)

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
		{name: "direct-target", parent: root.ID, target: &core.Target{Kind: "direct", Name: "fixture-author"}, want: http.StatusBadRequest},
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
	f := newReactionHTTPFixture(t)

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
	f := newReactionHTTPFixture(t)

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

func headerContext(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	return headerContextWithAuth(t, f, path, f.authorSpec)
}

func headerContextWithAuth(t *testing.T, f *reactionTransportFixture, path, token string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, f.httpURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, token)
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
