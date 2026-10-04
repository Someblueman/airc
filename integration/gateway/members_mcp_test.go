//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMembersReadThroughMCPTracksNativeMembership(t *testing.T) {
	f := newMCPHTTPFixture(t)
	socket := f.config.Bindings[0].Address
	credential, err := auth.ReadToken(f.config.Bindings[0].AccountTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	member := dial(t, socket, "fixture-author", credential, false, false)
	if err := member.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, member, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-author" })
	promoteRoomOperator(t, socket)
	request := core.Request{Version: 1, RequestID: "members-mcp", Operation: core.MembersRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	before := topicHistorySnapshot(t, socket)
	read := func(id string) core.MembersResult {
		request.RequestID = id
		call := mcpCallRequest(t, f.client, request)
		var result core.MembersResult
		if err := json.Unmarshal(mcpStructured(t, call), &result); err != nil {
			t.Fatal(err)
		}
		assertMembers(t, result, request.Target, nativeMembers(t, socket, request.Target.Name))
		return result
	}
	first := read("members-mcp")
	if !slices.Contains(first.Members, core.Member{Nick: "fixture-author", Operator: true}) || !slices.Contains(first.Members, core.Member{Nick: "fixture-observer"}) {
		t.Fatal("native MCP positive member controls", first)
	}
	if err := member.Part(request.Target.Name, ""); err != nil {
		t.Fatal(err)
	}
	next(t, member, func(e *irc.PartEvent) bool { return e.Agent == "fixture-author" })
	second := read("members-mcp-after-part")
	if slices.Contains(second.Members, core.Member{Nick: "fixture-author", Operator: true}) || len(second.Members) != len(first.Members)-1 {
		t.Fatal("MCP returned stale membership", first, second)
	}
	for _, bad := range []core.Request{
		{Version: 1, RequestID: "members-mcp-wrong", Operation: core.MembersRead, Target: &core.Target{Kind: "room", Name: "#private"}},
		{Version: 1, RequestID: "members-mcp-lookup", Operation: core.ReceiptGet, LookupID: "members-mcp"},
	} {
		call := mcpCallRequest(t, f.client, bad)
		if !call.IsError {
			t.Fatal("MCP disclosed forbidden room or fabricated read receipt", call.StructuredContent)
		}
	}
	if after := topicHistorySnapshot(t, socket); !sameSnapshot(before, after) {
		t.Fatal("MCP member read mutated history", before, after)
	}
}
