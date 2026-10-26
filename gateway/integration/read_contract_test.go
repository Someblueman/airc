//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

// readRoute is one read operation's HTTP GET route and its request equivalent.
type readRoute struct {
	name  string
	path  string
	query url.Values
	// request is the operation the route must map to.
	request core.Request
	// field is the read's JSON field, which no refusal may carry.
	field string
	pick  func(dispatch.DetachedResult) any
}

func readRoutes(parent, pollID string) []readRoute {
	room := contractRoom()
	targeted := func(extra ...string) url.Values {
		query := url.Values{"kind": {"room"}, "target": {room.Name}}
		for i := 0; i < len(extra); i += 2 {
			query.Set(extra[i], extra[i+1])
		}
		return query
	}
	return []readRoute{
		{"topic", "/v1/topic", targeted(), core.Request{Operation: core.TopicRead, Target: room}, "topic", func(r dispatch.DetachedResult) any { return r.Topic }},
		{"room", "/v1/room", targeted(), core.Request{Operation: core.RoomRead, Target: room}, "room", func(r dispatch.DetachedResult) any { return r.Room }},
		{"pins", "/v1/pins", targeted(), core.Request{Operation: core.PinsRead, Target: room}, "pins", func(r dispatch.DetachedResult) any { return r.Pins }},
		{"waiting", "/v1/waiting", targeted("parent_id", parent), core.Request{Operation: core.WaitingRead, Target: room, ParentID: parent}, "waiting", func(r dispatch.DetachedResult) any { return r.Waiting }},
		{"members", "/v1/members", targeted(), core.Request{Operation: core.MembersRead, Target: room}, "members", func(r dispatch.DetachedResult) any { return r.Members }},
		{"context", "/v1/context", targeted("parent_id", parent), core.Request{Operation: core.ContextRead, Target: room, ParentID: parent, Limit: 50}, "context", func(r dispatch.DetachedResult) any { return r.Context }},
		{"poll", "/v1/poll", targeted("poll_id", pollID), core.Request{Operation: core.PollResults, Target: room, PollID: pollID}, "poll", func(r dispatch.DetachedResult) any { return r.Poll }},
		{"channels", "/v1/channels", url.Values{}, core.Request{Operation: core.ChannelsRead}, "channels", func(r dispatch.DetachedResult) any { return r.Channels }},
		{"agents", "/v1/agents", url.Values{"limit": {"256"}}, core.Request{Operation: core.AgentsRead, Limit: core.MaxAgents}, "agents", func(r dispatch.DetachedResult) any { return r.Agents }},
		{"directory", "/v1/directory", url.Values{"limit": {"256"}}, core.Request{Operation: core.Directory, Limit: 256}, "directory", func(r dispatch.DetachedResult) any { return r.Directory }},
	}
}

// sameJSON reports whether a and b encode to the same JSON value.
func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	var left, right any
	for _, v := range []struct {
		in  any
		out *any
	}{{a, &left}, {b, &right}} {
		data, ok := v.in.([]byte)
		if !ok {
			var err error
			if data, err = json.Marshal(v.in); err != nil {
				t.Fatal(err)
			}
		}
		if err := json.Unmarshal(data, v.out); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
	}
	return reflect.DeepEqual(left, right)
}

// assertReadRefused requires status and a typed error body without read data.
func assertReadRefused(t *testing.T, label string, status, want int, body []byte, field string) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if status != want || json.Unmarshal(body, &envelope) != nil || envelope["error"] == nil || envelope[field] != nil {
		t.Fatalf("%s: status=%d want=%d body=%s", label, status, want, body)
	}
}

// TestReadRoutesMatchRequestsAndRefuseWithoutDisclosure checks every read's
// HTTP GET route maps to the same operation as the request route, and that
// unknown or repeated parameters, direct-message targets, foreign rooms and
// wrong credentials are refused with a typed error and no data. Reads never
// create a job or change native history.
func TestReadRoutesMatchRequestsAndRefuseWithoutDisclosure(t *testing.T) {
	f := newReactionHTTPFixture(t)
	root := seedReactionParent(t, f)
	seedTopic(t, f.socket, "read contract topic ☃")
	seedNativePin(t, f, root.ID)
	setReplySignalNative(t, f.socket, root.ID, &core.ReplySignalInput{Note: "read contract", TTLSeconds: 120})
	_, pollID := seedPollOnlyAt(t, f.socket, 120)
	before := topicHistorySnapshot(t, f.socket)
	for _, route := range readRoutes(root.ID, pollID) {
		path := route.path + "?" + route.query.Encode()
		status, body := headerRead(t, f, path)
		if status != http.StatusOK {
			t.Fatalf("%s GET status=%d body=%s", route.name, status, body)
		}
		request := route.request
		request.Version, request.RequestID = 1, "read-contract-"+route.name
		status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
		if status != http.StatusOK || result.Operation != request.Operation || !sameJSON(t, body, route.pick(result)) {
			t.Fatalf("%s GET differs from request route: status=%d GET=%s result=%+v", route.name, status, body, result)
		}
		if status, _ := headerRead(t, f, "/v1/requests/"+request.RequestID); status != http.StatusNotFound {
			t.Fatalf("%s read created a job: status=%d", route.name, status)
		}
		refuse := func(label string, query url.Values, want int) {
			t.Helper()
			status, body := headerRead(t, f, route.path+"?"+query.Encode())
			assertReadRefused(t, route.name+" "+label, status, want, body, route.field)
		}
		with := func(key string, values ...string) url.Values {
			query := url.Values{}
			for k, v := range route.query {
				query[k] = v
			}
			query[key] = values
			return query
		}
		refuse("unknown parameter", with("unknown", "x"), http.StatusBadRequest)
		if route.query.Has("target") {
			missingKind := with("kind")
			delete(missingKind, "kind")
			refuse("missing kind", missingKind, http.StatusBadRequest)
			refuse("repeated target", with("target", "#gateway-fixture", "#other-private"), http.StatusBadRequest)
			refuse("direct-message target", with("kind", "direct"), http.StatusBadRequest)
			refuse("foreign room", with("target", "#other-private"), http.StatusForbidden)
		}
		status, body = headerReadToken(t, f, path, wrongAuthorSpec)
		assertReadRefused(t, route.name+" wrong credential", status, http.StatusUnauthorized, body, route.field)
	}
	if after := topicHistorySnapshot(t, f.socket); !sameSnapshot(before, after) {
		t.Fatalf("reads changed native history: before=%v after=%v", before, after)
	}
}

// TestReadBoundsRefuseOutOfRangeWithoutPartialData checks the bounded reads'
// limits: out-of-range limits and byte budgets are refused, and a snapshot
// that does not fit its limit is refused whole instead of truncated.
func TestReadBoundsRefuseOutOfRangeWithoutPartialData(t *testing.T) {
	f := newReactionHTTPFixture(t)
	root := seedReactionParent(t, f)
	member := dial(t, f.socket, "bounds-member", "", false, false)
	if err := member.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, member, func(e *irc.JoinEvent) bool { return e.Agent == "bounds-member" })
	context := "/v1/context?" + url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "parent_id": {root.ID}}.Encode()
	history := "/v1/history?" + url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}}.Encode()
	for _, test := range []struct {
		path, field string
		status      int
	}{
		{context + "&limit=-1", "context", http.StatusBadRequest},
		{context + "&limit=1001", "context", http.StatusBadRequest},
		{context + "&max_bytes=512", "context", http.StatusBadRequest},
		{context + "&max_bytes=65537", "context", http.StatusBadRequest},
		{context + "&cursor=x", "context", http.StatusBadRequest},
		{context + "&start=recent", "context", http.StatusBadRequest},
		{context + "&from=fixture-author", "context", http.StatusBadRequest},
		{history + "&limit=0", "page", http.StatusBadRequest},
		{history + "&limit=1001", "page", http.StatusBadRequest},
		{"/v1/agents?limit=0", "agents", http.StatusBadRequest},
		{"/v1/agents?limit=257", "agents", http.StatusBadRequest},
		{"/v1/agents?limit=2&limit=3", "agents", http.StatusBadRequest},
		{"/v1/agents?target=%23gateway-fixture", "agents", http.StatusBadRequest},
		{"/v1/agents?limit=1", "agents", http.StatusTooManyRequests},
		{"/v1/directory?limit=0", "directory", http.StatusBadRequest},
		{"/v1/directory?limit=257", "directory", http.StatusBadRequest},
		{"/v1/directory?limit=2&limit=3", "directory", http.StatusBadRequest},
		{"/v1/directory?target=%23room", "directory", http.StatusBadRequest},
		{"/v1/directory?cursor=raw", "directory", http.StatusBadRequest},
		{"/v1/directory?limit=1", "directory", http.StatusTooManyRequests},
	} {
		status, body := headerRead(t, f, test.path)
		assertReadRefused(t, test.path, status, test.status, body, test.field)
	}
}
