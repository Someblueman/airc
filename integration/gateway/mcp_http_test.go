//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPHTTPListsAuthenticatedGatewayToolsAndSends(t *testing.T) {
	f := newMCPHTTPFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	tools, err := f.client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}
	if len(seen) != 31 || !seen[string(core.CapabilitiesGet)] || !seen[string(core.Send)] || !seen[string(core.MessageRetract)] {
		t.Fatalf("MCP tools=%d names=%v", len(seen), seen)
	}
	send := core.Request{Version: 1, RequestID: "mcp-http-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "MCP HTTP native send"}
	admitted := mcpResult(t, mcpCallRequest(t, f.client, send))
	if !admitted.Durable {
		t.Fatalf("MCP send was not a durable admission: %#v", admitted)
	}
	job := mcpAwaitAccepted(t, f, send)
	observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == job.MessageID })
	result := job
	if result.State != core.Accepted || result.MessageID == "" || observed.Message != send.Message || observed.Target != send.Target.Name || observed.From != "fixture-author" {
		t.Fatalf("MCP send result=%#v", result)
	}
	capRequest := core.Request{Version: 1, RequestID: "mcp-capabilities", Operation: core.CapabilitiesGet}
	capCall := mcpCallRequest(t, f.client, capRequest)
	if capCall.IsError {
		t.Fatalf("capabilities tool error=%#v", capCall.StructuredContent)
	}
	encoded, _ := json.Marshal(capCall.StructuredContent)
	if !strings.Contains(string(encoded), string(core.MessageCorrect)) || !strings.Contains(string(encoded), string(core.PollCreate)) {
		t.Fatalf("capabilities omitted implemented operations: %s", encoded)
	}
}

func TestMCPHTTPRejectsUnauthenticated(t *testing.T) {
	f := newMCPHTTPFixture(t)
	transport := &http.Transport{TLSClientConfig: f.cert.Client}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.address+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"unauth","version":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("unauthenticated MCP status=%d", response.StatusCode)
	}
}

func TestMCPHTTPExecutesAllGatewayOperations(t *testing.T) {
	f := newMCPHTTPFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := core.Request{Version: 1, RequestID: "mcp-all-send", Operation: core.Send, Target: room, Message: "MCP operation matrix parent"}
	parentAdmission := mcpResult(t, mcpCallRequest(t, f.client, parent))
	if !parentAdmission.Durable {
		t.Fatalf("parent admission=%#v", parentAdmission)
	}
	parentResult := mcpAwaitAccepted(t, f, parent)
	if parentResult.MessageID == "" {
		t.Fatalf("parent send=%#v", parentResult)
	}
	id := parentResult.MessageID
	call := func(r core.Request) core.Result {
		tool := mcpCallRequest(t, f.client, r)
		if tool.StructuredContent == nil {
			t.Fatalf("%s returned no structured content", r.Operation)
		}
		switch r.Operation {
		case core.ContextRead, core.PinsRead, core.RoomRead, core.WaitingRead, core.PollResults, core.TopicRead, core.MembersRead, core.HistoryRead, core.Search, core.Watch, core.Directory:
			mcpSnapshot(t, tool, r.Operation, r.Target)
			return core.Result{}
		}
		result := mcpResult(t, tool)
		switch r.Operation {
		case core.Send, core.Reply, core.ReactionAdd, core.ProfileUpdate, core.PresenceSet, core.TopicSet, core.RoomUpdate, core.PinsAdd, core.PinsRemove, core.ActivitySet, core.ReplySignalSet, core.ReplySignalCancel, core.PollCreate, core.PollVote, core.PollClose, core.MessageAction, core.MessageCorrect, core.MessageRetract:
			if !result.Durable {
				t.Fatalf("%s returned no durable result: %#v", r.Operation, tool.StructuredContent)
			}
			result = mcpAwaitAccepted(t, f, r)
			if result.Accepted == nil || !*result.Accepted || result.SendAttempts != 1 || result.LookupAttempts != 0 {
				t.Fatalf("%s accepted result facts: %+v", r.Operation, result)
			}
			switch r.Operation {
			case core.ProfileUpdate, core.PresenceSet:
				if result.Card == nil || result.Card.Operation != r.Operation {
					t.Fatalf("%s missing card acknowledgement: %+v", r.Operation, result)
				}
			case core.TopicSet:
				if result.Topic == nil || result.Topic.Operation != r.Operation {
					t.Fatalf("%s missing topic acknowledgement: %+v", r.Operation, result)
				}
			case core.RoomUpdate:
				if result.Room == nil || result.Room.Operation != r.Operation {
					t.Fatalf("%s missing room acknowledgement: %+v", r.Operation, result)
				}
			case core.PinsAdd, core.PinsRemove:
				if result.Pin == nil || result.Pin.Operation != r.Operation {
					t.Fatalf("%s missing pin acknowledgement: %+v", r.Operation, result)
				}
			case core.ActivitySet:
				if result.Activity == nil || result.Activity.Operation != r.Operation {
					t.Fatalf("%s missing activity acknowledgement: %+v", r.Operation, result)
				}
			case core.ReplySignalSet, core.ReplySignalCancel:
				if result.ReplySignal == nil || result.ReplySignal.Operation != r.Operation {
					t.Fatalf("%s missing reply signal acknowledgement: %+v", r.Operation, result)
				}
			case core.PollCreate, core.PollVote, core.PollClose:
				if result.Poll == nil || result.Poll.Operation != r.Operation {
					t.Fatalf("%s missing poll acknowledgement: %+v", r.Operation, result)
				}
			}
			switch r.Operation {
			case core.Send, core.Reply, core.ReactionAdd, core.MessageAction, core.MessageCorrect, core.MessageRetract:
				if result.MessageID == "" {
					t.Fatalf("%s missing accepted message id: %+v", r.Operation, result)
				}
				observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
				if observed.Target != r.Target.Name || observed.From != "fixture-author" {
					t.Fatalf("%s native mismatch: %+v", r.Operation, observed)
				}
				switch r.Operation {
				case core.Reply:
					if observed.ReplyTo != r.ParentID || observed.Message != r.Message {
						t.Fatalf("reply native fields: %+v", observed)
					}
				case core.ReactionAdd:
					if observed.Reaction != r.Reaction || observed.Message != r.Reaction || observed.ReplyTo != r.ParentID {
						t.Fatalf("reaction native fields: %+v", observed)
					}
				case core.MessageAction, core.MessageCorrect:
					if observed.Message != r.Message {
						t.Fatalf("message mutation native body: %+v", observed)
					}
				}
				if r.Operation == core.MessageRetract && (observed.Message != "Retracted" || observed.Retracted) {
					t.Fatalf("retract native facts: %+v", observed)
				}
			}
		case core.ReceiptGet:
			if result.State != core.Accepted || result.MessageID == "" || result.SendAttempts != 1 || result.LookupAttempts != 0 {
				t.Fatalf("receipt lookup lost original acceptance facts: %+v", result)
			}
		default:
			encoded, _ := json.Marshal(tool.StructuredContent)
			if len(encoded) < 2 {
				t.Fatalf("%s returned empty snapshot", r.Operation)
			}
		}
		return result
	}
	call(core.Request{Version: 1, RequestID: "mcp-all-reply", Operation: core.Reply, Target: room, ParentID: id, Message: "MCP reply"})
	call(core.Request{Version: 1, RequestID: "mcp-all-react", Operation: core.ReactionAdd, Target: room, ParentID: id, Reaction: "agree"})
	call(core.Request{Version: 1, RequestID: "mcp-all-context", Operation: core.ContextRead, Target: room, ParentID: id, Limit: 10})
	call(core.Request{Version: 1, RequestID: "mcp-all-pins", Operation: core.PinsRead, Target: room})
	call(core.Request{Version: 1, RequestID: "mcp-all-room", Operation: core.RoomRead, Target: room})
	call(core.Request{Version: 1, RequestID: "mcp-all-waiting", Operation: core.WaitingRead, Target: room, ParentID: id})
	call(core.Request{Version: 1, RequestID: "mcp-all-topic", Operation: core.TopicRead, Target: room})
	call(core.Request{Version: 1, RequestID: "mcp-all-members", Operation: core.MembersRead, Target: room})
	call(core.Request{Version: 1, RequestID: "mcp-all-history", Operation: core.HistoryRead, Target: room, Limit: 10})
	call(core.Request{Version: 1, RequestID: "mcp-all-search", Operation: core.Search, Target: room, Query: "operation", Limit: 10})
	call(core.Request{Version: 1, RequestID: "mcp-all-directory", Operation: core.Directory, Query: "fixture-author", Limit: 10})
	call(core.Request{Version: 1, RequestID: "mcp-all-receipt", Operation: core.ReceiptGet, LookupID: parent.RequestID})
	call(core.Request{Version: 1, RequestID: "mcp-all-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: strptr("MCP matrix")}})
	call(core.Request{Version: 1, RequestID: "mcp-all-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", Note: "MCP matrix", TTLSeconds: 30}})
	topicSet := call(core.Request{Version: 1, RequestID: "mcp-all-topic-set", Operation: core.TopicSet, Target: room, Topic: strptr("MCP topic")})
	if topicSet.Topic == nil || topicSet.Topic.Topic.Topic != "MCP topic" || topicSet.Topic.Topic.Target != *room {
		t.Fatalf("topic acknowledgement lost whole value: %+v", topicSet.Topic)
	}
	slow, historyLimit := int64(0), 32
	roomUpdate := call(core.Request{Version: 1, RequestID: "mcp-all-room-update", Operation: core.RoomUpdate, Target: room, Room: &core.RoomPatch{SlowSeconds: &slow, HistoryLimit: &historyLimit}})
	if roomUpdate.Room == nil || roomUpdate.Room.Room.Target != *room || roomUpdate.Room.Room.SlowSeconds != 0 || roomUpdate.Room.Room.HistoryLimit != 32 {
		t.Fatalf("room acknowledgement lost whole value: %+v", roomUpdate.Room)
	}
	call(core.Request{Version: 1, RequestID: "mcp-all-pin-add", Operation: core.PinsAdd, Target: room, ParentID: id})
	pinsAfterAdd := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-all-pins-after-add", Operation: core.PinsRead, Target: room})
	var added core.PinsResult
	if err := json.Unmarshal(mcpStructured(t, pinsAfterAdd), &added); err != nil || len(added.Pins) != 1 || added.Pins[0].ID != id || added.Pins[0].Message != "MCP operation matrix parent" {
		t.Fatalf("pins.add was not reflected in typed snapshot: %+v", added)
	}
	call(core.Request{Version: 1, RequestID: "mcp-all-pin-remove", Operation: core.PinsRemove, Target: room, ParentID: id})
	pinsAfterRemove := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-all-pins-after-remove", Operation: core.PinsRead, Target: room})
	var removed core.PinsResult
	if err := json.Unmarshal(mcpStructured(t, pinsAfterRemove), &removed); err != nil || len(removed.Pins) != 0 {
		t.Fatalf("pins.remove was not reflected in typed snapshot: %+v", removed)
	}
	call(core.Request{Version: 1, RequestID: "mcp-all-activity", Operation: core.ActivitySet, Target: room, Activity: &core.ActivityInput{Kind: "typing", Note: "MCP", TTLSeconds: 5}})
	call(core.Request{Version: 1, RequestID: "mcp-all-signal-set", Operation: core.ReplySignalSet, Target: room, ParentID: id, ReplySignal: &core.ReplySignalInput{Note: "MCP", TTLSeconds: 5}})
	call(core.Request{Version: 1, RequestID: "mcp-all-signal-cancel", Operation: core.ReplySignalCancel, Target: room, ParentID: id})
	poll := call(core.Request{Version: 1, RequestID: "mcp-all-poll-create", Operation: core.PollCreate, Target: room, Poll: &core.PollInput{Question: "MCP poll", Options: []string{"yes", "no"}, TTLSeconds: 60}})
	if poll.Poll == nil {
		t.Fatalf("poll create=%#v", poll)
	}
	pollID := poll.Poll.Poll.ID
	vote := call(core.Request{Version: 1, RequestID: "mcp-all-poll-vote", Operation: core.PollVote, Target: room, PollID: pollID, Choice: 1})
	if vote.Poll == nil || len(vote.Poll.Poll.Votes) != 2 || vote.Poll.Poll.Votes[0] != 1 {
		t.Fatalf("poll vote acknowledgement lost tally: %+v", vote.Poll)
	}
	pollResults := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-all-poll-results", Operation: core.PollResults, Target: room, PollID: pollID})
	var results core.PollResult
	if err := json.Unmarshal(mcpStructured(t, pollResults), &results); err != nil || results.ID != pollID || len(results.Votes) != 2 || results.Votes[0] != 1 {
		t.Fatalf("poll results lost vote: %+v", results)
	}
	closed := call(core.Request{Version: 1, RequestID: "mcp-all-poll-close", Operation: core.PollClose, Target: room, PollID: pollID})
	if closed.Poll == nil || !closed.Poll.Poll.Closed {
		t.Fatalf("poll close acknowledgement not closed: %+v", closed.Poll)
	}
	action := core.Request{Version: 1, RequestID: "mcp-all-action", Operation: core.MessageAction, Target: room, Message: "MCP action"}
	actionResult := call(action)
	call(core.Request{Version: 1, RequestID: "mcp-all-correct", Operation: core.MessageCorrect, Target: room, ParentID: actionResult.MessageID, Message: "MCP corrected"})
	retract := core.Request{Version: 1, RequestID: "mcp-all-retract", Operation: core.MessageRetract, Target: room, ParentID: actionResult.MessageID}
	retractResult := call(retract)
	historyCall := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-all-annotated", Operation: core.HistoryRead, Target: room, Limit: 64})
	var history backend.HistoryPage
	if err := json.Unmarshal(mcpStructured(t, historyCall), &history); err != nil {
		t.Fatal(err)
	}
	annotated := false
	for _, message := range history.Messages {
		if message.ID == actionResult.MessageID && message.Retracted && message.SupersededBy == retractResult.MessageID {
			annotated = true
		}
	}
	if !annotated {
		t.Fatalf("retraction did not annotate original action: action=%s retract=%s history=%+v", actionResult.MessageID, retractResult.MessageID, history)
	}
	searchCall := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-all-search-proof", Operation: core.Search, Target: room, Query: "operation", Limit: 64})
	var searched backend.HistoryPage
	if err := json.Unmarshal(mcpStructured(t, searchCall), &searched); err != nil || len(searched.Messages) == 0 {
		t.Fatalf("search returned no matching whole records: %+v", searched)
	}
	foundParent := false
	for _, message := range searched.Messages {
		if message.ID == id && message.Message == parent.Message {
			foundParent = true
		}
	}
	if !foundParent {
		t.Fatalf("search omitted parent whole record: %+v", searched.Messages)
	}
	directoryCall := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-all-directory-proof", Operation: core.Directory, Query: "fixture-author", Limit: 10})
	var directory backend.DirectorySnapshot
	if err := json.Unmarshal(mcpStructured(t, directoryCall), &directory); err != nil || len(directory.Agents) != 1 || directory.Agents[0].Nick != "fixture-author" {
		t.Fatalf("directory omitted queried author: %+v", directory)
	}
}

func strptr(v string) *string { return &v }

func TestMCPHTTPWatchDeliversProgressPagesAndCancellation(t *testing.T) {
	progress := make(chan *mcp.ProgressNotificationClientRequest, 32)
	f := newMCPHTTPFixture(t, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		select {
		case progress <- req:
		default:
		}
	}})
	request := core.Request{Version: 1, RequestID: "mcp-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 10, Start: "recent"}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	params := &mcp.CallToolParams{Name: string(core.Watch), Arguments: json.RawMessage(body)}
	params.SetProgressToken("mcp-watch-progress")
	watchCtx, cancel := context.WithCancel(t.Context())
	done := make(chan struct {
		result *mcp.CallToolResult
		err    error
	}, 1)
	go func() {
		result, err := f.client.CallTool(watchCtx, params)
		done <- struct {
			result *mcp.CallToolResult
			err    error
		}{result, err}
	}()
	select {
	case notification := <-progress:
		if notification.Params.ProgressToken != "mcp-watch-progress" || notification.Params.Message == "" || notification.Params.Progress <= 0 {
			t.Fatalf("progress=%#v", notification)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch emitted no initial progress page")
	}
	author := dial(t, f.config.Bindings[0].Address, "mcp-watch-peer", "", false, true)
	liveBody := "MCP live watch 🙂"
	if err := author.SendWithID("#gateway-fixture", liveBody, "mcp-watch-live"); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.SendReceiptEvent) bool {
		return e.RequestID == "mcp-watch-live" && e.Receipt != nil && e.Receipt.Accepted
	})
	progressSeen, live := false, false
	encodedLive := base64.RawURLEncoding.EncodeToString([]byte(liveBody))
	deadline := time.After(5 * time.Second)
	for !progressSeen || !live {
		select {
		case notification := <-progress:
			if notification.Params.ProgressToken != "mcp-watch-progress" || notification.Params.Message == "" || notification.Params.Progress <= 0 {
				t.Fatalf("live progress token=%#v", notification)
			}
			progressSeen = true
			live = strings.Contains(notification.Params.Message, encodedLive)
		case <-deadline:
			t.Fatal("watch emitted no live progress page")
		}
	}
	cancel()
	select {
	case outcome := <-done:
		if outcome.err == nil && (outcome.result == nil || !outcome.result.IsError) {
			t.Fatalf("watch cancellation returned success: %#v", outcome.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch cancellation did not join")
	}
}

func TestMCPHTTPWatchRenewsAndResumesSealedCursor(t *testing.T) {
	progress := make(chan *mcp.ProgressNotificationClientRequest, 64)
	f := newMCPHTTPFixture(t, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		select {
		case progress <- req:
		default:
		}
	}})
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	seed := dial(t, f.config.Bindings[0].Address, "mcp-renew-seed", "", false, true)
	for i, body := range []string{"MCP renew seed one", "MCP renew seed two"} {
		requestID := "mcp-renew-seed-" + strconv.Itoa(i)
		if err := seed.SendWithID(room.Name, body, requestID); err != nil {
			t.Fatal(err)
		}
		next(t, seed, func(e *irc.SendReceiptEvent) bool {
			return e.RequestID == requestID && e.Receipt != nil && e.Receipt.Accepted
		})
	}
	watch := core.Request{Version: 1, RequestID: "mcp-renew-watch", Operation: core.Watch, Target: room, Limit: 128, Start: "retained"}
	body, err := json.Marshal(watch)
	if err != nil {
		t.Fatal(err)
	}
	params := &mcp.CallToolParams{Name: string(core.Watch), Arguments: json.RawMessage(body)}
	params.SetProgressToken("mcp-renew-progress")
	renewCtx, cancel := context.WithTimeout(t.Context(), 23*time.Second)
	defer cancel()
	renewed, err := f.client.CallTool(renewCtx, params)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.IsError {
		t.Fatalf("renewal returned tool error: %#v", renewed.StructuredContent)
	}
	var renewal struct {
		Reason string              `json:"reason"`
		Page   backend.HistoryPage `json:"page"`
	}
	if data := mcpStructured(t, renewed); json.Unmarshal(data, &renewal) != nil || renewal.Reason != "stream_renew" || renewal.Page.Cursor == "" {
		t.Fatalf("watch did not return sealed renewal: %#v", renewed.StructuredContent)
	}
	for len(progress) > 0 {
		<-progress
	}
	peer := dial(t, f.config.Bindings[0].Address, "mcp-renew-peer", "", false, true)
	liveBody := "MCP sealed cursor resume 🙂"
	resume := watch
	resume.RequestID = "mcp-renew-resume"
	resume.Cursor = renewal.Page.Cursor
	resume.Start = ""
	resumeBody, err := json.Marshal(resume)
	if err != nil {
		t.Fatal(err)
	}
	resumeParams := &mcp.CallToolParams{Name: string(core.Watch), Arguments: json.RawMessage(resumeBody)}
	resumeParams.SetProgressToken("mcp-resume-progress")
	resumeCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	resumeDone := make(chan error, 1)
	go func() {
		_, callErr := f.client.CallTool(resumeCtx, resumeParams)
		resumeDone <- callErr
	}()
	select {
	case notification := <-progress:
		if notification.Params.ProgressToken != "mcp-resume-progress" {
			t.Fatalf("resumed watch progress token=%#v", notification)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resumed sealed cursor emitted no initial page")
	}
	if err := peer.SendWithID(room.Name, liveBody, "mcp-renew-live"); err != nil {
		t.Fatal(err)
	}
	next(t, peer, func(e *irc.SendReceiptEvent) bool {
		return e.RequestID == "mcp-renew-live" && e.Receipt != nil && e.Receipt.Accepted
	})
	encoded := base64.RawURLEncoding.EncodeToString([]byte(liveBody))
	seen := false
	deadline := time.After(5 * time.Second)
	for !seen {
		select {
		case notification := <-progress:
			if strings.Contains(notification.Params.Message, encoded) {
				seen = true
			}
		case <-deadline:
			t.Fatal("resumed sealed cursor omitted live message")
		}
	}
	stop()
	select {
	case <-resumeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("resumed watch did not cancel")
	}
}
