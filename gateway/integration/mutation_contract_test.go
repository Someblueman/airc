//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// The mutation contract is the behaviour every durable mutation shares,
// whatever its operation: durable HTTP admission, one native effect however
// often or wherever the request is replayed, a conflict for changed intent,
// and refusal before admission. Operation-specific mapping lives in each
// operation's own HTTP test.

// mutationCase is one mutation operation in the shared contract tests.
type mutationCase struct {
	name string
	// request seeds the native state the mutation needs and returns it.
	request func(t *testing.T, f *reactionTransportFixture) core.Request
	// changed is the same request ID with different intent.
	changed func(*core.Request)
	// perturb changes native state after acceptance, so that a re-executed
	// replay would be visible in state.
	perturb func(t *testing.T, f *reactionTransportFixture, r core.Request)
	// state snapshots the native state the mutation owns.
	state func(t *testing.T, f *reactionTransportFixture, r core.Request) any
}

func contractRoom() *core.Target { return &core.Target{Kind: "room", Name: "#gateway-fixture"} }

func mutationContractCases() []mutationCase {
	parentRequest := func(op core.Operation, seed string, input func(*core.Request)) func(*testing.T, *reactionTransportFixture) core.Request {
		return func(t *testing.T, f *reactionTransportFixture) core.Request {
			r := core.Request{Version: 1, Operation: op, Target: contractRoom(), ParentID: seedPinMutationMessage(t, f.socket, "#gateway-fixture", seed)}
			if input != nil {
				input(&r)
			}
			return r
		}
	}
	otherParent := func(r *core.Request) { r.ParentID = strings.Repeat("a", 32) }
	waiting := func(t *testing.T, f *reactionTransportFixture, r core.Request) any {
		return queryNativeWaitingSignal(t, f.socket, r.ParentID)
	}
	poll := func(t *testing.T, f *reactionTransportFixture, r core.Request) any {
		return nativePollEntry(t, f.socket, r.PollID)
	}
	seededPoll := func(op core.Operation, choice int) func(*testing.T, *reactionTransportFixture) core.Request {
		return func(t *testing.T, f *reactionTransportFixture) core.Request {
			_, id := seedPollOnlyAt(t, f.socket, 120)
			return core.Request{Version: 1, Operation: op, Target: contractRoom(), PollID: id, Choice: choice}
		}
	}
	return []mutationCase{
		{
			name:    "reaction-add",
			request: parentRequest(core.ReactionAdd, "contract-reaction-parent", func(r *core.Request) { r.Reaction = "agree" }),
			changed: func(r *core.Request) { r.Reaction = "seen" },
		},
		{
			name: "message-action",
			request: func(*testing.T, *reactionTransportFixture) core.Request {
				return core.Request{Version: 1, Operation: core.MessageAction, Target: contractRoom(), Message: "contract action"}
			},
			changed: func(r *core.Request) { r.Message = "changed action" },
		},
		{
			name:    "message-correct",
			request: parentRequest(core.MessageCorrect, "contract-correct-parent", func(r *core.Request) { r.Message = "contract correction" }),
			changed: func(r *core.Request) { r.Message = "changed correction" },
		},
		{
			name:    "message-retract",
			request: parentRequest(core.MessageRetract, "contract-retract-parent", nil),
			changed: otherParent,
		},
		{
			name:    "pins-add",
			request: parentRequest(core.PinsAdd, "contract-pin-parent", nil),
			changed: otherParent,
			perturb: func(t *testing.T, f *reactionTransportFixture, r core.Request) {
				nativePinMutation(t, f.socket, r.Target.Name, r.ParentID, "unpin")
			},
		},
		{
			name: "pins-remove",
			request: func(t *testing.T, f *reactionTransportFixture) core.Request {
				r := parentRequest(core.PinsRemove, "contract-unpin-parent", nil)(t, f)
				nativePinMutation(t, f.socket, r.Target.Name, r.ParentID, "pin")
				return r
			},
			changed: otherParent,
			perturb: func(t *testing.T, f *reactionTransportFixture, r core.Request) {
				nativePinMutation(t, f.socket, r.Target.Name, r.ParentID, "pin")
			},
		},
		{
			name: "poll-create",
			request: func(*testing.T, *reactionTransportFixture) core.Request {
				return pollMutationRequest("", core.PollCreate, contractRoom(), "")
			},
			changed: func(r *core.Request) {
				r.Poll = &core.PollInput{Question: "Changed?", Options: []string{"a", "b"}, TTLSeconds: 120}
			},
		},
		{
			name:    "poll-vote",
			request: seededPoll(core.PollVote, 2),
			changed: func(r *core.Request) { r.Choice = 1 },
			perturb: func(t *testing.T, f *reactionTransportFixture, r core.Request) {
				pollNativeVote(t, f.socket, r.PollID, 1)
			},
			state: poll,
		},
		{
			name:    "poll-close",
			request: seededPoll(core.PollClose, 0),
			changed: func(r *core.Request) { r.PollID = strings.Repeat("a", 32) },
			state:   poll,
		},
		{
			name: "topic-set",
			request: func(*testing.T, *reactionTransportFixture) core.Request {
				topic := "contract topic ☃"
				return core.Request{Version: 1, Operation: core.TopicSet, Target: contractRoom(), Topic: &topic}
			},
			changed: func(r *core.Request) { other := "changed topic"; r.Topic = &other },
			perturb: func(t *testing.T, f *reactionTransportFixture, _ core.Request) {
				seedTopic(t, f.socket, "native topic")
			},
			state: func(t *testing.T, f *reactionTransportFixture, r core.Request) any {
				return queryNativeTopic(t, f.socket, r.Target.Name)
			},
		},
		{
			name: "room-update",
			request: func(t *testing.T, f *reactionTransportFixture) core.Request {
				promoteRoomOperator(t, f.socket)
				slow, limit := int64(0), 30
				return core.Request{Version: 1, Operation: core.RoomUpdate, Target: contractRoom(), Room: &core.RoomPatch{SlowSeconds: &slow, HistoryLimit: &limit}}
			},
			changed: func(r *core.Request) { limit := 29; r.Room = &core.RoomPatch{HistoryLimit: &limit} },
			perturb: func(t *testing.T, f *reactionTransportFixture, r core.Request) {
				admin := roomWaitingAdmin(t, f)
				defer admin.Close()
				if err := admin.Chat(irc.ChatRequest{Action: "room", Target: r.Target.Name, Seconds: 0, Limit: 31}); err != nil {
					t.Fatal(err)
				}
				next(t, admin, func(e *irc.ChatEvent) bool { return e.Action == "room" && e.HistoryLimit == 31 })
				next(t, admin, func(e *irc.EndOfChatEvent) bool { return true })
			},
			state: func(t *testing.T, f *reactionTransportFixture, r core.Request) any {
				return queryNativeRoom(t, f.socket, r.Target.Name)
			},
		},
		{
			// A re-executed activity would broadcast once the native two-second
			// throttle has passed; the replay phase waits for that.
			name: "activity-set",
			request: func(*testing.T, *reactionTransportFixture) core.Request {
				return activityRequest("", "typing", "contract activity", 15)
			},
			changed: func(r *core.Request) { r.Activity = &core.ActivityInput{Kind: "thinking", TTLSeconds: 15} },
		},
		{
			name:    "reply-signal-set",
			request: parentRequest(core.ReplySignalSet, "contract-signal-parent", func(r *core.Request) { r.ReplySignal = &core.ReplySignalInput{Note: "coming soon", TTLSeconds: 120} }),
			changed: func(r *core.Request) { r.ReplySignal = &core.ReplySignalInput{Note: "changed", TTLSeconds: 120} },
			perturb: func(t *testing.T, f *reactionTransportFixture, r core.Request) {
				setReplySignalNative(t, f.socket, r.ParentID, nil)
			},
			state: waiting,
		},
		{
			name: "reply-signal-cancel",
			request: func(t *testing.T, f *reactionTransportFixture) core.Request {
				r := parentRequest(core.ReplySignalCancel, "contract-cancel-parent", nil)(t, f)
				setReplySignalNative(t, f.socket, r.ParentID, &core.ReplySignalInput{Note: "before cancel", TTLSeconds: 120})
				return r
			},
			changed: otherParent,
			perturb: func(t *testing.T, f *reactionTransportFixture, r core.Request) {
				setReplySignalNative(t, f.socket, r.ParentID, &core.ReplySignalInput{Note: "native after cancel", TTLSeconds: 120})
			},
			state: waiting,
		},
		{
			name: "profile-update",
			request: func(*testing.T, *reactionTransportFixture) core.Request {
				model := "contract-model"
				return core.Request{Version: 1, Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model}}
			},
			changed: func(r *core.Request) { model := "changed-model"; r.Profile = &core.ProfilePatch{Model: &model} },
			perturb: func(t *testing.T, f *reactionTransportFixture, _ core.Request) {
				author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
				defer author.Close()
				if err := author.UpdateProfile(map[string]string{"model": "native-model"}); err != nil {
					t.Fatal(err)
				}
				next(t, author, func(e *irc.EndOfDirectoryEvent) bool { return true })
			},
			state: func(t *testing.T, f *reactionTransportFixture, _ core.Request) any {
				return nativeCard(t, f.socket, "fixture-author").AgentProfile
			},
		},
		{
			name: "presence-set",
			request: func(*testing.T, *reactionTransportFixture) core.Request {
				return core.Request{Version: 1, Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "thinking", Note: "contract presence", TTLSeconds: 60}}
			},
			changed: func(r *core.Request) { r.Presence = &core.PresenceInput{State: "away", TTLSeconds: 60} },
			perturb: func(t *testing.T, f *reactionTransportFixture, _ core.Request) {
				author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
				defer author.Close()
				if err := author.SetPresence("away", "native presence", time.Minute); err != nil {
					t.Fatal(err)
				}
				next(t, author, func(e *irc.EndOfDirectoryEvent) bool { return true })
			},
			state: func(t *testing.T, f *reactionTransportFixture, _ core.Request) any {
				card := nativeCard(t, f.socket, "fixture-author")
				return [2]string{card.State, card.Note}
			},
		},
	}
}

// admitMutationHTTP posts r with author headers and requires durable admission.
func admitMutationHTTP(t *testing.T, f *reactionTransportFixture, method, path string, r core.Request) core.Result {
	t.Helper()
	status, result, location := headerReactionHTTP(t, f, method, path, requestJSON(t, r))
	if status != http.StatusAccepted || !result.Durable || result.RequestID != r.RequestID || location != "/v1/requests/"+r.RequestID {
		t.Fatalf("%s %s admission: status=%d location=%q result=%+v", r.Operation, method, status, location, result)
	}
	return result
}

// headerPost posts r to /v1/messages with author headers and requires
// durable admission.
func headerPost(t *testing.T, f *reactionTransportFixture, r core.Request) core.Result {
	t.Helper()
	return admitMutationHTTP(t, f, http.MethodPost, "/v1/messages", r)
}

// TestMutationsReplayDurablyAcrossRestart admits every mutation once through
// either HTTP route, changes native state behind it, then replays each request
// before and after a gateway restart: the journal returns the saved result
// without dispatching again, and a changed intent under the same request ID
// conflicts.
func TestMutationsReplayDurablyAcrossRestart(t *testing.T) {
	f := newReactionFixture(t, true, nil, true)
	observer := newReplySignalObserver(t, f.socket, *contractRoom())
	cases := mutationContractCases()
	requests := make([]core.Request, len(cases))
	saved := make([]core.Result, len(cases))
	var lastAccepted time.Time
	for i, c := range cases {
		r := c.request(t, f)
		r.RequestID = "contract-" + c.name
		// Alternate the two HTTP admission routes; every replay below uses the
		// author-header route, so half the replays also cross routes.
		if i%2 == 0 {
			admitMutationHTTP(t, f, http.MethodPost, "/v1/messages", r)
		} else if status, admission := reactionHTTPCall(t, f, f.cred.token(t, r, true)); status != http.StatusAccepted || admission.Result == nil || !admission.Result.Durable || admission.Result.RequestID != r.RequestID {
			t.Fatalf("%s request-route admission: status=%d result=%+v", c.name, status, admission)
		}
		result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
		lastAccepted = time.Now()
		if !result.Durable || result.Accepted == nil || !*result.Accepted || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.AcceptanceUncertain || result.DuplicateRisk {
			t.Fatalf("%s accepted facts: %+v", c.name, result)
		}
		requests[i], saved[i] = r, result
	}
	for i, c := range cases {
		if c.perturb != nil {
			c.perturb(t, f, requests[i])
		}
	}
	snapshot := func() []any {
		states := []any{topicHistorySnapshot(t, f.socket), queryPinMutationNative(t, f.socket, "#gateway-fixture")}
		for i, c := range cases {
			if c.state != nil {
				states = append(states, c.state(t, f, requests[i]))
			}
		}
		return states
	}
	drainObserver(observer)
	before := snapshot()
	replay := func(phase, method string) {
		t.Helper()
		for i, r := range requests {
			path := "/v1/messages"
			if method == http.MethodPut {
				path = "/v1/requests/" + r.RequestID
			}
			admitMutationHTTP(t, f, method, path, r)
			if got := waitHTTPPinResult(t, f, r.RequestID, core.Accepted); !reflect.DeepEqual(got, saved[i]) {
				t.Fatalf("%s %s replay changed the saved result:\n got=%+v\nwant=%+v", phase, cases[i].name, got, saved[i])
			}
		}
	}
	replay("live", http.MethodPost)
	f.restart(t)
	// Native activity and signal writes inside the two-second throttle are
	// invisible; wait it out so any re-executed write would broadcast.
	time.Sleep(time.Until(lastAccepted.Add(2100 * time.Millisecond)))
	replay("restarted", http.MethodPut)
	for i, c := range cases {
		changed := requests[i]
		c.changed(&changed)
		if status, _, _ := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, changed)); status != http.StatusConflict {
			t.Fatalf("%s changed intent status=%d, want 409", c.name, status)
		}
	}
	assertNoNativeMutation(t, observer)
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("replays changed native state:\nbefore=%+v\n after=%+v", before, after)
	}
}

// drainObserver discards events already delivered to observer.
func drainObserver(observer *irc.Client) {
	for {
		select {
		case _, ok := <-observer.Events():
			if !ok {
				return
			}
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
}

// assertNoNativeMutation fails if observer sees any message, signal, topic or
// chat broadcast within a short quiet window.
func assertNoNativeMutation(t *testing.T, observer *irc.Client) {
	t.Helper()
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("native observer disconnected")
			}
			switch event.(type) {
			case *irc.MessageEvent, *irc.SignalEvent, *irc.TopicEvent, *irc.ChatEvent:
				t.Fatalf("unexpected native mutation: %#v", event)
			}
		case <-timer.C:
			return
		}
	}
}

// TestMutationRefusalsCreateNoJob sends invalid intent and unauthorized
// targets for every mutation: each is refused at admission and leaves no job.
func TestMutationRefusalsCreateNoJob(t *testing.T) {
	f := newReactionFixture(t, false, nil, true)
	room := *contractRoom()
	parent := strings.Repeat("a", 32)
	text := func(s string) *string { return &s }
	foreign := map[string]func(*core.Request){
		"message":  func(r *core.Request) { r.Message = "body" },
		"reaction": func(r *core.Request) { r.Reaction = "agree" },
		"parent":   func(r *core.Request) { r.ParentID = parent },
		"cursor":   func(r *core.Request) { r.Cursor = "cursor" },
		"limit":    func(r *core.Request) { r.Limit = 1 },
		"query":    func(r *core.Request) { r.Query = "query" },
		"thread":   func(r *core.Request) { r.ThreadMode = "thread" },
		"topic":    func(r *core.Request) { r.Topic = text("topic") },
		"profile":  func(r *core.Request) { r.Profile = &core.ProfilePatch{Clear: true} },
		"presence": func(r *core.Request) { r.Presence = &core.PresenceInput{State: "away", TTLSeconds: 1} },
		"poll":     func(r *core.Request) { r.PollID = strings.Repeat("b", 32) },
		"room":     func(r *core.Request) { n := int64(0); r.Room = &core.RoomPatch{SlowSeconds: &n} },
		"activity": func(r *core.Request) { r.Activity = &core.ActivityInput{Kind: "typing", TTLSeconds: 1} },
	}
	missingTarget := func(r *core.Request) { r.Target = nil }
	directSelf := func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-author"} }
	slow := int64(1)
	oversized := strings.Repeat("<", 400)
	for _, op := range []struct {
		base    core.Request
		foreign []string
		invalid map[string]func(*core.Request)
		denied  []core.Target
	}{
		{
			base:    activityRequest("", "typing", "valid", 15),
			foreign: []string{"parent", "message", "reaction", "cursor", "topic", "profile", "presence", "poll", "room"},
			invalid: map[string]func(*core.Request){
				"missing-input": func(r *core.Request) { r.Activity = nil }, "missing-target": missingTarget,
				"wrong-kind": func(r *core.Request) { r.Activity.Kind = "prepare" }, "ttl-zero": func(r *core.Request) { r.Activity.TTLSeconds = 0 },
				"ttl-high": func(r *core.Request) { r.Activity.TTLSeconds = 16 }, "note-high": func(r *core.Request) { r.Activity.Note = strings.Repeat("x", 241) },
				"note-control": func(r *core.Request) { r.Activity.Note = "bad\nline" },
			},
			denied: []core.Target{{Kind: "room", Name: "#other-private"}, {Kind: "direct", Name: "activity-private"}},
		},
		{
			base:    core.Request{Operation: core.PinsAdd, Target: &room, ParentID: parent},
			foreign: []string{"message", "reaction", "poll", "topic", "room", "profile", "presence", "cursor", "limit", "query", "thread"},
			invalid: map[string]func(*core.Request){"missing-parent": func(r *core.Request) { r.ParentID = "" }, "bad-parent": func(r *core.Request) { r.ParentID = "invalid" }, "missing-target": missingTarget, "direct": directSelf},
			denied:  []core.Target{{Kind: "room", Name: "#other-private"}},
		},
		{
			base:    core.Request{Operation: core.PinsRemove, Target: &room, ParentID: parent},
			foreign: []string{"message", "reaction", "poll", "topic", "room", "profile", "presence", "cursor", "limit", "query", "thread"},
			invalid: map[string]func(*core.Request){"missing-parent": func(r *core.Request) { r.ParentID = "" }, "bad-parent": func(r *core.Request) { r.ParentID = "invalid" }, "missing-target": missingTarget, "direct": directSelf},
		},
		{
			base:    pollMutationRequest("", core.PollCreate, &room, ""),
			foreign: []string{"message", "parent"},
			invalid: map[string]func(*core.Request){"direct": func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-peer"} }, "wrong-input": func(r *core.Request) { r.Poll = nil }},
		},
		{
			base:    pollMutationRequest("", core.PollVote, &room, parent),
			foreign: []string{"message", "parent"},
			invalid: map[string]func(*core.Request){
				"direct": func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-peer"} },
				"wrong-input": func(r *core.Request) {
					r.Poll = &core.PollInput{Question: "foreign", Options: []string{"a", "b"}, TTLSeconds: 1}
				},
			},
		},
		{
			base:    pollMutationRequest("", core.PollClose, &room, parent),
			foreign: []string{"message", "parent"},
			invalid: map[string]func(*core.Request){
				"direct": func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-peer"} },
				"wrong-input": func(r *core.Request) {
					r.Poll = &core.PollInput{Question: "foreign", Options: []string{"a", "b"}, TTLSeconds: 1}
				},
			},
		},
		{
			base:    replySignalRequest("", parent, core.ReplySignalSet, &room),
			foreign: []string{"message", "reaction", "cursor", "activity", "topic"},
			invalid: map[string]func(*core.Request){
				"missing-parent": func(r *core.Request) { r.ParentID = "" }, "bad-parent": func(r *core.Request) { r.ParentID = "bad" }, "missing-target": missingTarget,
				"wrong-input": func(r *core.Request) { r.ReplySignal = nil }, "ttl-zero": func(r *core.Request) { r.ReplySignal.TTLSeconds = 0 },
				"ttl-high": func(r *core.Request) { r.ReplySignal.TTLSeconds = 901 }, "long-note": func(r *core.Request) { r.ReplySignal.Note = strings.Repeat("x", 241) },
				"control": func(r *core.Request) { r.ReplySignal.Note = "bad\nline" },
			},
			denied: []core.Target{{Kind: "room", Name: "#signal-private"}, {Kind: "direct", Name: "signal-private"}},
		},
		{
			base:    replySignalRequest("", parent, core.ReplySignalCancel, &room),
			foreign: []string{"message", "reaction", "cursor", "activity", "topic"},
			invalid: map[string]func(*core.Request){
				"missing-parent": func(r *core.Request) { r.ParentID = "" }, "bad-parent": func(r *core.Request) { r.ParentID = "bad" }, "missing-target": missingTarget,
				"wrong-input": func(r *core.Request) { r.ReplySignal = &core.ReplySignalInput{TTLSeconds: 1} },
			},
			denied: []core.Target{{Kind: "room", Name: "#signal-private"}, {Kind: "direct", Name: "signal-private"}},
		},
		{
			base:    core.Request{Operation: core.RoomUpdate, Target: &room, Room: &core.RoomPatch{SlowSeconds: &slow}},
			foreign: []string{"message", "reaction", "parent", "topic", "profile", "presence", "cursor", "limit"},
			invalid: map[string]func(*core.Request){
				"omitted": func(r *core.Request) { r.Room = nil }, "empty": func(r *core.Request) { r.Room = &core.RoomPatch{} },
				"negative-slow":    func(r *core.Request) { n := int64(-1); r.Room = &core.RoomPatch{SlowSeconds: &n} },
				"negative-history": func(r *core.Request) { n := -1; r.Room = &core.RoomPatch{HistoryLimit: &n} },
				"slow-3601":        func(r *core.Request) { n := int64(3601); r.Room = &core.RoomPatch{SlowSeconds: &n} },
				"direct":           directSelf, "missing-target": missingTarget,
			},
			denied: []core.Target{{Kind: "room", Name: "#other-private"}},
		},
		{
			base:    core.Request{Operation: core.TopicSet, Target: &room, Topic: text("valid topic")},
			foreign: []string{"message", "reaction", "parent"},
			invalid: map[string]func(*core.Request){"oversized": func(r *core.Request) { r.Topic = text(strings.Repeat("x", 401)) }, "control": func(r *core.Request) { r.Topic = text("bad\x00topic") }},
			denied:  []core.Target{{Kind: "room", Name: "#other-private"}},
		},
		{
			base: core.Request{Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: text("valid model")}},
			invalid: map[string]func(*core.Request){
				"oversized-patch": func(r *core.Request) {
					r.Profile = &core.ProfilePatch{Model: &oversized, Workspace: &oversized, Tools: &oversized, About: &oversized}
				},
				"foreign-target": func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-observer"} },
			},
		},
		{
			base:    core.Request{Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", TTLSeconds: 60}},
			invalid: map[string]func(*core.Request){"foreign-target": func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-observer"} }},
		},
		{
			base: core.Request{Operation: core.ReactionAdd, Target: &room, ParentID: parent, Reaction: "agree"},
			invalid: map[string]func(*core.Request){
				"malformed-reaction": func(r *core.Request) { r.Reaction = "bad reaction" }, "direct": directSelf,
				"malformed-parent": func(r *core.Request) { r.ParentID = "not-a-message-id" },
			},
			denied: []core.Target{{Kind: "room", Name: "#other-private"}},
		},
	} {
		refuse := func(name string, mutate func(*core.Request), want int) {
			t.Helper()
			r := op.base
			r.Version = 1
			r.RequestID = strings.ReplaceAll(string(r.Operation), ".", "-") + "-refused-" + name
			if r.Activity != nil {
				activity := *r.Activity
				r.Activity = &activity
			}
			if r.ReplySignal != nil {
				signal := *r.ReplySignal
				r.ReplySignal = &signal
			}
			mutate(&r)
			if status, _, _ := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r)); status != want {
				t.Fatalf("%s %s status=%d, want %d", r.Operation, name, status, want)
			}
			if status, body := headerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
				t.Fatalf("%s %s refusal created a job: status=%d body=%s", r.Operation, name, status, body)
			}
		}
		for _, name := range op.foreign {
			refuse("foreign-"+name, foreign[name], http.StatusBadRequest)
		}
		for name, mutate := range op.invalid {
			refuse(name, mutate, http.StatusBadRequest)
		}
		for _, target := range op.denied {
			refuse("denied-"+target.Kind, func(r *core.Request) { r.Target = &target }, http.StatusForbidden)
		}
	}
	if card := nativeCard(t, f.socket, "fixture-author"); card.AgentProfile != (irc.AgentProfile{}) || card.State != "unknown" {
		t.Fatalf("refused card requests changed the native author: %+v", card)
	}
}

// TestMutationNativeMuteIsBlockedWithoutEffect mutes the author natively: every
// write the native server refuses is classified Blocked/Forbidden with its
// numeric, changes nothing, and succeeds again once unmuted.
func TestMutationNativeMuteIsBlockedWithoutEffect(t *testing.T) {
	f := newReactionFixture(t, true, nil, true)
	observer := newReplySignalObserver(t, f.socket, *contractRoom())
	parent := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "mute-parent")
	retracted := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "mute-retract-parent")
	nativePinMutation(t, f.socket, "#gateway-fixture", parent, "pin")
	setReplySignalNative(t, f.socket, parent, &core.ReplySignalInput{Note: "before mute", TTLSeconds: 120})
	requests := []core.Request{
		activityRequest("", "typing", "muted note", 15),
		messageMutationRequest("", core.MessageAction, contractRoom(), ""),
		messageMutationRequest("", core.MessageCorrect, contractRoom(), parent),
		messageMutationRequest("", core.MessageRetract, contractRoom(), retracted),
		{Version: 1, Operation: core.PinsRemove, Target: contractRoom(), ParentID: parent},
		replySignalRequest("", parent, core.ReplySignalCancel, contractRoom()),
	}
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	moderate := func(action string) {
		t.Helper()
		if err := admin.Moderate(irc.AdminRequest{Action: action, Nick: "fixture-author", Scope: "#gateway-fixture"}); err != nil {
			t.Fatal(err)
		}
		if ack := next(t, admin, func(e *irc.AdminEvent) bool { return e.Action == action }); !ack.Changed {
			t.Fatal("native moderation made no change", ack)
		}
		next(t, admin, func(e *irc.EndOfAdminEvent) bool { return true })
	}
	state := func() []any {
		return []any{topicHistorySnapshot(t, f.socket), queryPinMutationNative(t, f.socket, "#gateway-fixture"), queryNativeWaitingSignal(t, f.socket, parent)}
	}
	drainObserver(observer)
	before := state()
	moderate("mute")
	for _, r := range requests {
		r.RequestID = "muted-" + strings.ReplaceAll(string(r.Operation), ".", "-")
		admitMutationHTTP(t, f, http.MethodPost, "/v1/messages", r)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden || blocked.Failure.UpstreamCode != 485 ||
			blocked.MessageID != "" || blocked.Activity != nil || blocked.Pin != nil || blocked.ReplySignal != nil {
			t.Fatalf("%s native mute classification: %+v", r.Operation, blocked)
		}
	}
	assertNoNativeMutation(t, observer)
	if after := state(); !reflect.DeepEqual(before, after) {
		t.Fatalf("muted writes changed native state:\nbefore=%+v\n after=%+v", before, after)
	}
	moderate("unmute")
	for _, r := range requests {
		r.RequestID = "unmuted-" + strings.ReplaceAll(string(r.Operation), ".", "-")
		admitMutationHTTP(t, f, http.MethodPost, "/v1/messages", r)
		if accepted := waitHTTPPinResult(t, f, r.RequestID, core.Accepted); accepted.Accepted == nil || !*accepted.Accepted || accepted.SendAttempts != 1 {
			t.Fatalf("%s after unmute: %+v", r.Operation, accepted)
		}
	}
	if pins := queryPinMutationNative(t, f.socket, "#gateway-fixture"); len(pins) != 0 {
		t.Fatalf("unmuted unpin did not apply: %+v", pins)
	}
	if signals := queryNativeWaitingSignal(t, f.socket, parent); len(signals) != 0 {
		t.Fatalf("unmuted cancel did not apply: %+v", signals)
	}
}
