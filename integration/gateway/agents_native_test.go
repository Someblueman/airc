//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func nativeAgents(t *testing.T, socket string) []irc.AgentInfo {
	t.Helper()
	probe := dial(t, socket, "agents-probe", "", false, true)
	defer probe.Close()
	if err := probe.Agents(); err != nil {
		t.Fatal(err)
	}
	agents := make([]irc.AgentInfo, 0, 16)
	for {
		select {
		case event, ok := <-probe.Events():
			if !ok {
				t.Fatal("native AGENTS observer closed")
			}
			switch value := event.(type) {
			case *irc.AgentsEvent:
				agents = append(agents, value.Agent)
			case *irc.EndOfAgentsEvent:
				return agents
			}
		case <-time.After(5 * time.Second):
			t.Fatal("native AGENTS timed out")
		}
	}
}

func joinAgentsMember(t *testing.T, socket string) *irc.Client {
	t.Helper()
	member := dial(t, socket, "agents-member", "", false, false)
	if err := member.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, member, func(e *irc.JoinEvent) bool { return e.Agent == "agents-member" && e.Channel == "#gateway-fixture" })
	if err := member.Join("#Gateway-Fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, member, func(e *irc.JoinEvent) bool { return e.Agent == "agents-member" && e.Channel == "#Gateway-Fixture" })
	return member
}

func assertAgentsResult(t *testing.T, got, want []irc.AgentInfo) {
	t.Helper()
	if len(got) != len(want) || !equalAgents(got, want) {
		t.Fatalf("agents snapshot=%+v want=%+v", got, want)
	}
}

func equalAgents(left, right []irc.AgentInfo) bool {
	return len(left) == len(right) && jsonEqual(left, right)
}

// Registration, JOIN, and OBSERVE acknowledgements fence native membership
// changes. Query once after those fences; disconnect still needs bounded polling.
func requireNativeAgent(t *testing.T, socket, nick string, present bool) {
	t.Helper()
	agents := nativeAgents(t, socket)
	found := false
	for _, agent := range agents {
		found = found || agent.Nick == nick
	}
	if found != present {
		t.Fatalf("native AGENTS membership for %q=%t want=%t: %+v", nick, found, present, agents)
	}
}

func waitNativeAgentAbsent(t *testing.T, socket, nick string) []irc.AgentInfo {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		agents := nativeAgents(t, socket)
		found := false
		for _, agent := range agents {
			found = found || agent.Nick == nick
		}
		if !found {
			return agents
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("native AGENTS still contained %q", nick)
	return nil
}

func jsonEqual(left, right any) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return string(a) == string(b)
}

func agentsHeaderHTTP(t *testing.T, f httpFixture, limit string) (int, []irc.AgentInfo) {
	t.Helper()
	status, _, data := httpCall(t, f, http.MethodGet, "/v1/agents?limit="+limit, f.token, nil)
	var result core.AgentsResult
	if status == http.StatusOK && json.Unmarshal(data, &result) != nil {
		t.Fatalf("agents HTTP JSON=%s", data)
	}
	return status, result.Agents
}
