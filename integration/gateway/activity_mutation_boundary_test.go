//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestActivityMutationInvalidAndUnauthorizedIntentCreatesNoJob(t *testing.T) {
	f := newReactionTransportFixture(t)
	for name, mutate := range map[string]func(*core.Request){
		"missing-input":  func(r *core.Request) { r.Activity = nil },
		"missing-target": func(r *core.Request) { r.Target = nil },
		"wrong-kind":     func(r *core.Request) { r.Activity.Kind = "prepare" },
		"ttl-zero":       func(r *core.Request) { r.Activity.TTLSeconds = 0 },
		"ttl-high":       func(r *core.Request) { r.Activity.TTLSeconds = 16 },
		"note-high":      func(r *core.Request) { r.Activity.Note = strings.Repeat("x", 241) },
		"note-control":   func(r *core.Request) { r.Activity.Note = "bad\nline" },
		"parent":         func(r *core.Request) { r.ParentID = strings.Repeat("a", 32) },
		"message":        func(r *core.Request) { r.Message = "body" },
		"reaction":       func(r *core.Request) { r.Reaction = "agree" },
		"cursor":         func(r *core.Request) { r.Cursor = "cursor" },
		"topic":          func(r *core.Request) { s := "topic"; r.Topic = &s },
		"profile":        func(r *core.Request) { r.Profile = &core.ProfilePatch{Clear: true} },
		"presence":       func(r *core.Request) { r.Presence = &core.PresenceInput{State: "away", TTLSeconds: 1} },
		"poll":           func(r *core.Request) { r.PollID = strings.Repeat("a", 32) },
		"room":           func(r *core.Request) { s := int64(0); r.Room = &core.RoomPatch{SlowSeconds: &s} },
	} {
		r := activityRequest("activity-invalid-"+name, "typing", "valid", 15)
		mutate(&r)
		status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r))
		if status != http.StatusBadRequest {
			t.Fatalf("%s invalid admission status=%d", name, status)
		}
		if status, _ := bearerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
			t.Fatalf("%s invalid intent created job: %d", name, status)
		}
	}
	for _, target := range []core.Target{{Kind: "room", Name: "#other-private"}, {Kind: "direct", Name: "activity-private"}} {
		observer := newActivityObserver(t, f.socket, target)
		r := activityRequest("activity-unauthorized-"+target.Kind, "thinking", "forbidden", 15)
		r.Target = &target
		status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r))
		if status != http.StatusForbidden {
			t.Fatalf("unauthorized %s status=%d", target.Kind, status)
		}
		if status, _ := bearerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
			t.Fatalf("unauthorized intent created job: %d", status)
		}
		assertNoActivitySignal(t, observer)
	}
}

func TestActivityMutationNativeMuteAndRecovery(t *testing.T) {
	f := newReactionTransportFixtureWithAdmin(t, true)
	r := activityRequest("activity-muted", "typing", "muted note", 15)
	observer := newActivityObserver(t, f.socket, *r.Target)
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	moderate := func(action string) {
		if err := admin.Moderate(irc.AdminRequest{Action: action, Nick: "fixture-author", Scope: r.Target.Name}); err != nil {
			t.Fatal(err)
		}
		if ack := next(t, admin, func(e *irc.AdminEvent) bool { return e.Action == action }); !ack.Changed {
			t.Fatal("native moderation made no change", ack)
		}
		next(t, admin, func(e *irc.EndOfAdminEvent) bool { return true })
	}
	moderate("mute")
	roomBearerPost(t, f, r, 1)
	blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
	if blocked.Activity != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden || blocked.Failure.UpstreamCode != 485 {
		t.Fatalf("native mute facts: %+v", blocked)
	}
	assertNoActivitySignal(t, observer)
	moderate("unmute")
	r.RequestID = "activity-unmuted"
	roomBearerPost(t, f, r, 1)
	assertActivityMutationFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextActivitySignal(t, observer))
}
