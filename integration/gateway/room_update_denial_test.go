//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRoomUpdateRejectsInvalidIntentBeforeAdmission(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	slow := int64(1)
	original := core.Request{Version: 1, Operation: core.RoomUpdate, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Room: &core.RoomPatch{SlowSeconds: &slow}}
	for name, mutate := range map[string]func(*core.Request){
		"omitted":          func(r *core.Request) { r.Room = nil },
		"empty":            func(r *core.Request) { r.Room = &core.RoomPatch{} },
		"negative-slow":    func(r *core.Request) { n := int64(-1); r.Room = &core.RoomPatch{SlowSeconds: &n} },
		"negative-history": func(r *core.Request) { n := -1; r.Room = &core.RoomPatch{HistoryLimit: &n} },
		"slow-3601":        func(r *core.Request) { n := int64(3601); r.Room = &core.RoomPatch{SlowSeconds: &n} },
		"message":          func(r *core.Request) { r.Message = "body" },
		"reaction":         func(r *core.Request) { r.Reaction = "agree" },
		"parent":           func(r *core.Request) { r.ParentID = strings.Repeat("a", 32) },
		"topic":            func(r *core.Request) { s := "topic"; r.Topic = &s },
		"profile":          func(r *core.Request) { r.Profile = &core.ProfilePatch{Clear: true} },
		"presence":         func(r *core.Request) { r.Presence = &core.PresenceInput{State: "away", TTLSeconds: 1} },
		"cursor":           func(r *core.Request) { r.Cursor = "cursor" },
		"limit":            func(r *core.Request) { r.Limit = 1 },
		"direct":           func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-author"} },
		"missing-target":   func(r *core.Request) { r.Target = nil },
	} {
		r := original
		r.RequestID = "room-bad-" + name
		mutate(&r)
		status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r))
		if status != http.StatusBadRequest {
			t.Fatalf("%s intent status=%d", name, status)
		}
		if status, _ := bearerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
			t.Fatalf("%s created a job: status=%d", name, status)
		}
	}
	foreign := original
	foreign.RequestID = "room-unauthorized-target"
	foreign.Target = &core.Target{Kind: "room", Name: "#other-private"}
	status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, foreign))
	if status != http.StatusForbidden {
		t.Fatalf("foreign room status=%d", status)
	}
	if status, _ := bearerRead(t, f, "/v1/requests/"+foreign.RequestID); status != http.StatusNotFound {
		t.Fatalf("foreign room created a job: status=%d", status)
	}
}
