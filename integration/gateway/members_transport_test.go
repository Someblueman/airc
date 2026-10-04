//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMembersReadAcrossEncryptedTransportsAndBearer(t *testing.T) {
	f := newReactionTransportFixtureWithTargets(t, true, []core.Target{{Kind: "room", Name: "#members-unknown"}})
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	const room = "#gateway-fixture"
	if err := f.observer.Part(room, ""); err != nil {
		t.Fatal(err)
	}
	next(t, f.observer, func(e *irc.PartEvent) bool { return e.Agent == "fixture-observer" })
	if err := f.observer.Observe(room); err != nil {
		t.Fatal(err)
	}
	next(t, f.observer, func(e *irc.RawEvent) bool { return e.Command == "765" })
	operator := dial(t, f.socket, "fixture-author", fixtureAccount, false, false)
	if err := operator.Join(room); err != nil {
		t.Fatal(err)
	}
	next(t, operator, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-author" })
	promoteRoomOperator(t, f.socket)
	for _, nick := range []string{"members-alpha", "members-beta"} {
		member := dial(t, f.socket, nick, "", false, false)
		if err := member.Join(room); err != nil {
			t.Fatal(err)
		}
		next(t, member, func(e *irc.JoinEvent) bool { return e.Agent == nick })
	}
	want := nativeMembers(t, f.socket, room)
	if !slices.Contains(want, core.Member{Nick: "fixture-author", Operator: true}) || !slices.Contains(want, core.Member{Nick: "members-alpha"}) || !slices.Contains(want, core.Member{Nick: "members-beta"}) || slices.Contains(want, core.Member{Nick: "fixture-observer"}) {
		t.Fatal("native visibility/operator control failed", want)
	}
	before := topicHistorySnapshot(t, f.socket)
	if len(want) < 3 {
		t.Fatalf("native NAMES did not expose seeded members: %+v", want)
	}
	request := core.Request{Version: 1, RequestID: "members-read-encrypted", Operation: core.MembersRead, Target: &core.Target{Kind: "room", Name: room}}
	cases := []struct {
		name string
		call func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, f, token, true) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return contextFramedCall(t, f.framed, token, "members-framed", true)
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return contextWebSocketCall(t, f.ws, token, "members-websocket", true)
		}},
	}
	for _, tc := range cases {
		status, result := tc.call(f.cred.token(t, request, true))
		if status != http.StatusOK || result.Members == nil {
			t.Fatalf("%s members response: status=%d result=%+v", tc.name, status, result)
		}
		assertMembers(t, *result.Members, request.Target, want)
	}
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	status, signed := contextHTTPCall(t, &h2, f.cred.token(t, request, false), false)
	if status != http.StatusOK || signed.Members == nil {
		t.Fatalf("signed HTTP/2 members response: status=%d result=%+v", status, signed)
	}
	assertMembers(t, *signed.Members, request.Target, want)
	query := "?kind=room&target=" + url.QueryEscape(room)
	status, body := bearerMembers(t, &h2, query, f.bearer)
	var bearer core.MembersResult
	if status != http.StatusOK || json.Unmarshal(body, &bearer) != nil {
		t.Fatalf("bearer members response: status=%d body=%s", status, body)
	}
	assertMembers(t, bearer, request.Target, want)
	if status, _ := bearerMembers(t, f, query+"&unknown=x", f.bearer); status != http.StatusBadRequest {
		t.Fatalf("unknown members query status=%d", status)
	}
	if status, _ := bearerMembers(t, f, query, "invalid-token"); status != http.StatusUnauthorized {
		t.Fatalf("invalid members credential status=%d", status)
	}
	if status, _ := bearerMembers(t, f, "?kind=room&target=%23private", f.bearer); status != http.StatusForbidden {
		t.Fatalf("foreign members target status=%d", status)
	}
	if status, body := bearerMembers(t, f, "?kind=room&target=%23members-unknown", f.bearer); status != http.StatusOK {
		t.Fatalf("unknown room members status=%d body=%s", status, body)
	} else {
		var empty core.MembersResult
		if err := json.Unmarshal(body, &empty); err != nil || empty.Members == nil || len(empty.Members) != 0 {
			t.Fatalf("unknown room members was not an explicit empty snapshot: %s", body)
		}
	}
	if status, _ := bearerMembers(t, f, query, f.bearer); status != http.StatusOK {
		t.Fatalf("members snapshot changed on repeat: status=%d", status)
	}
	lookupRequest, err := http.NewRequest(http.MethodGet, f.httpURL+"/v1/requests/members-read-encrypted", nil)
	if err != nil {
		t.Fatal(err)
	}
	lookupRequest.Header.Set("Authorization", "Bearer "+f.bearer)
	response, err := f.http.Do(lookupRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if after := topicHistorySnapshot(t, f.socket); !sameSnapshot(before, after) {
		t.Fatal("members reads mutated history", before, after)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("members read created a delivery job: status=%d", response.StatusCode)
	}
}

func bearerMembers(t *testing.T, f *reactionTransportFixture, query, token string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, f.httpURL+"/v1/members"+query, nil)
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
	data, err := io.ReadAll(io.LimitReader(response.Body, core.MaxMembersBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func nativeMembers(t *testing.T, socket, room string) []core.Member {
	t.Helper()
	probe := dial(t, socket, "members-probe", "", false, true)
	defer probe.Close()
	if err := probe.Names(room); err != nil {
		t.Fatal(err)
	}
	members := map[string]core.Member{}
	for {
		event := next(t, probe, func(e *irc.RawEvent) bool { return e.Command == "353" || e.Command == "366" })
		if len(event.Params) < 2 || event.Params[0] != probe.Nick() || event.Params[len(event.Params)-1] != room {
			t.Fatal("foreign native members evidence", event)
		}
		if event.Command == "366" {
			break
		}
		for _, raw := range strings.Fields(event.Trailing) {
			operator := strings.HasPrefix(raw, "@")
			nick := strings.TrimPrefix(raw, "@")
			if nick != "" {
				members[nick] = core.Member{Nick: nick, Operator: operator}
			}
		}
	}
	result := make([]core.Member, 0, len(members))
	for _, member := range members {
		result = append(result, member)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Nick) < strings.ToLower(result[j].Nick) })
	return result
}

func assertMembers(t *testing.T, got core.MembersResult, target *core.Target, want []core.Member) {
	t.Helper()
	if target == nil || got.Target != *target || got.Members == nil {
		t.Fatalf("members target/list=%+v want target=%+v", got, target)
	}
	if !slices.Equal(got.Members, want) {
		t.Fatalf("members snapshot=%+v want=%+v", got.Members, want)
	}
}
