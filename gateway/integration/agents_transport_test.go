//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestAgentsReadHTTPMembership maps agents.read to the native AGENTS list:
// joined members with their rooms, never passive observers, hidden sessions or
// disconnected durable cards, and the same list through both HTTP routes.
func TestAgentsReadHTTPMembership(t *testing.T) {
	t.Parallel()
	f := httpGatewayFixture(t, false, nil, false, true)
	member := joinAgentsMember(t, f.socket)
	passive := dial(t, f.socket, "agents-passive", "", false, false)
	requireNativeAgent(t, f.socket, passive.Nick(), true)
	if err := passive.Observe("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, passive, func(e *irc.RawEvent) bool { return e.Command == "765" })
	requireNativeAgent(t, f.socket, passive.Nick(), false)
	hidden := dial(t, f.socket, "agents-hidden", "", false, true)
	if err := hidden.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	requireNativeAgent(t, f.socket, "agents-member", true)
	if err := member.SendWithID("#gateway-fixture", "agent discovery history control", "agents-history-control"); err != nil {
		t.Fatal(err)
	}
	next(t, f.observer, func(e *irc.MessageEvent) bool {
		return e.From == member.Nick() && e.Message == "agent discovery history control"
	})
	want := nativeAgents(t, f.socket)
	memberFound, hiddenFound := false, false
	for _, agent := range want {
		memberFound = memberFound || (agent.Nick == "agents-member" && jsonEqual(agent.Channels, []string{"#Gateway-Fixture", "#gateway-fixture"}) && !agent.ConnectedAt.IsZero())
		hiddenFound = hiddenFound || agent.Nick == "agents-hidden" || agent.Nick == passive.Nick()
		if agent.Nick == "fixture-author" {
			t.Fatalf("disconnected durable card leaked into AGENTS: %+v", want)
		}
	}
	if !memberFound || hiddenFound {
		t.Fatalf("native AGENTS membership/visibility=%+v", want)
	}
	before := nativeWebTransportMessages(t, f.socket, "#gateway-fixture")
	if len(before) != 1 || before[0].Message != "agent discovery history control" {
		t.Fatalf("native history control=%+v", before)
	}
	status, got := agentsHeaderHTTP(t, f, "256")
	if status != http.StatusOK {
		t.Fatalf("agents authorSpec status=%d", status)
	}
	assertAgentsResult(t, got, want)
	status, _, data := httpCall(t, f, http.MethodGet, "/v1/agents", f.token, nil)
	var defaults core.AgentsResult
	if status != http.StatusOK || json.Unmarshal(data, &defaults) != nil || defaults.Version != 1 || !reflect.DeepEqual(defaults.Agents, want) {
		t.Fatalf("default agents bound status=%d body=%s", status, data)
	}
	request := core.Request{Version: 1, RequestID: "agents-request", Operation: core.AgentsRead, Limit: core.MaxAgents}
	status, result := postAuthorRequest(t, f, request)
	if status != http.StatusOK || result.Agents == nil || result.Agents.Version != 1 {
		t.Fatalf("agents request status=%d result=%+v", status, result)
	}
	assertAgentsResult(t, result.Agents.Agents, want)
	if _, err := f.journal.LookupAuthor(t.Context(), f.identity, "agents"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("agents read created job: %v", err)
	}
	_ = member.Close()
	waitNativeAgentAbsent(t, f.socket, "agents-member")
	status, got = agentsHeaderHTTP(t, f, "256")
	if status != http.StatusOK {
		t.Fatal(status)
	}
	assertAgentsResult(t, got, nativeAgents(t, f.socket))
	if !reflect.DeepEqual(before, nativeWebTransportMessages(t, f.socket, "#gateway-fixture")) {
		t.Fatal("agent discovery changed native history")
	}
}
