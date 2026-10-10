//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReplySignalInvalidIntentAndUnauthorizedTargetCreatesNoJob(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		for name, mutate := range map[string]func(*core.Request){
			"missing-parent": func(r *core.Request) { r.ParentID = "" }, "bad-parent": func(r *core.Request) { r.ParentID = "bad" }, "missing-target": func(r *core.Request) { r.Target = nil },
			"message": func(r *core.Request) { r.Message = "body" }, "reaction": func(r *core.Request) { r.Reaction = "agree" }, "cursor": func(r *core.Request) { r.Cursor = "cursor" },
			"activity": func(r *core.Request) { r.Activity = &core.ActivityInput{Kind: "typing", TTLSeconds: 1} }, "topic": func(r *core.Request) { s := "topic"; r.Topic = &s },
			"wrong-input": func(r *core.Request) {
				if op == core.ReplySignalSet {
					r.ReplySignal = nil
				} else {
					r.ReplySignal = &core.ReplySignalInput{TTLSeconds: 1}
				}
			},
		} {
			r := replySignalRequest("signal-invalid-"+strings.ReplaceAll(string(op), ".", "-")+"-"+name, strings.Repeat("a", 32), op, target)
			mutate(&r)
			status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r))
			if status != http.StatusBadRequest {
				t.Fatalf("%s %s invalid status=%d", op, name, status)
			}
			if status, _ := bearerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
				t.Fatalf("invalid signal created job: %d", status)
			}
		}
		for _, forbidden := range []core.Target{{Kind: "room", Name: "#signal-private"}, {Kind: "direct", Name: "signal-private"}} {
			r := replySignalRequest("signal-denied-"+strings.ReplaceAll(string(op), ".", "-")+"-"+forbidden.Kind, strings.Repeat("a", 32), op, &forbidden)
			status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r))
			if status != http.StatusForbidden {
				t.Fatalf("unauthorized signal status=%d", status)
			}
			if status, _ := bearerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
				t.Fatalf("denied signal created job: %d", status)
			}
		}
	}
	for name, input := range map[string]*core.ReplySignalInput{"zero": {TTLSeconds: 0}, "high": {TTLSeconds: 901}, "long-note": {TTLSeconds: 1, Note: strings.Repeat("x", 241)}, "control": {TTLSeconds: 1, Note: "bad\nline"}} {
		r := replySignalRequest("signal-invalid-"+name, strings.Repeat("a", 32), core.ReplySignalSet, target)
		r.ReplySignal = input
		status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r))
		if status != http.StatusBadRequest {
			t.Fatalf("invalid input %s status=%d", name, status)
		}
		if status, _ := bearerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
			t.Fatalf("invalid input created job: %d", status)
		}
	}
}

func TestReplySignalForeignRoomMuteAndRetainedNoOpCancel(t *testing.T) {
	f := newReactionTransportFixtureWithAdmin(t, true)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	foreignTarget := core.Target{Kind: "room", Name: "#signal-foreign"}
	foreignObserver := newReplySignalObserver(t, f.socket, foreignTarget)
	foreign := seedPinMutationMessage(t, f.socket, foreignTarget.Name, "signal-foreign-room")
	setReplySignalNative(t, f.socket, foreign, &core.ReplySignalInput{Note: "foreign unchanged", TTLSeconds: 120})
	nextReplySignal(t, foreignObserver, foreign)
	before := queryNativeWaitingSignal(t, f.socket, foreign)
	if len(before) != 1 {
		t.Fatal("native foreign positive control missing", before)
	}
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		r := replySignalRequest("signal-forged-"+strings.ReplaceAll(string(op), ".", "-"), foreign, op, target)
		roomBearerPost(t, f, r, 1)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.ReplySignal != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
			t.Fatalf("foreign room proof allowed write: %+v", blocked)
		}
		if after := queryNativeWaitingSignal(t, f.socket, foreign); !reflect.DeepEqual(before, after) {
			t.Fatalf("forged signal changed native state: before=%+v after=%+v", before, after)
		}
		assertNoReplySignal(t, foreignObserver)
	}
	parent := seedPinMutationMessage(t, f.socket, target.Name, "signal-mute-parent")
	observer := newReplySignalObserver(t, f.socket, *target)
	r := replySignalRequest("signal-before-mute", parent, core.ReplySignalSet, target)
	roomBearerPost(t, f, r, 1)
	assertReplySignalFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextReplySignal(t, observer, parent))
	before = queryNativeWaitingSignal(t, f.socket, parent)
	if len(before) != 1 {
		t.Fatal("native signal positive control missing", before)
	}
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	moderate := func(action string) {
		if err := admin.Moderate(irc.AdminRequest{Action: action, Nick: "fixture-author", Scope: target.Name}); err != nil {
			t.Fatal(err)
		}
		if a := next(t, admin, func(e *irc.AdminEvent) bool { return e.Action == action }); !a.Changed {
			t.Fatal("native moderation made no change", a)
		}
		next(t, admin, func(e *irc.EndOfAdminEvent) bool { return true })
	}
	moderate("mute")
	r = replySignalRequest("signal-muted-cancel", parent, core.ReplySignalCancel, target)
	roomBearerPost(t, f, r, 1)
	blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
	if blocked.ReplySignal != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.UpstreamCode != 485 || blocked.Failure.Code != core.Forbidden {
		t.Fatalf("native mute did not block signal: %+v", blocked)
	}
	if after := queryNativeWaitingSignal(t, f.socket, parent); !reflect.DeepEqual(before, after) {
		t.Fatal("muted cancel changed signal", after)
	}
	assertNoReplySignal(t, observer)
	moderate("unmute")
	for _, id := range []string{"signal-unmuted-cancel", "signal-retained-noop-cancel"} {
		r.RequestID = id
		roomBearerPost(t, f, r, 1)
		assertReplySignalFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextReplySignal(t, observer, parent))
		if entries := queryNativeWaitingSignal(t, f.socket, parent); len(entries) != 0 {
			t.Fatal("native cancellation did not clear signal", entries)
		}
	}
}
