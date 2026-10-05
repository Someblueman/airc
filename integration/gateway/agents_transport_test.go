//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAgentsReadHTTPBearerSignedEncryptedAndMembership(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http%d", map[bool]int{false: 1, true: 2}[h2]), func(t *testing.T) {
			f := httpGatewayFixture(t, h2, nil, false, true)
			member := joinAgentsMember(t, f.socket)
			passive := dial(t, f.socket, "agents-passive", "", false, false)
			waitNativeAgent(t, f.socket, passive.Nick())
			if err := passive.Observe("#gateway-fixture"); err != nil {
				t.Fatal(err)
			}
			next(t, passive, func(e *irc.RawEvent) bool { return e.Command == "765" })
			waitNativeAgentAbsent(t, f.socket, passive.Nick())
			hidden := dial(t, f.socket, "agents-hidden", "", false, true)
			if err := hidden.Join("#gateway-fixture"); err != nil {
				t.Fatal(err)
			}
			waitNativeAgent(t, f.socket, "agents-member")
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
			status, got := agentsBearerHTTP(t, f, "256")
			if status != http.StatusOK {
				t.Fatalf("agents bearer status=%d", status)
			}
			assertAgentsResult(t, got, want)
			status, _, data := httpCall(t, f, http.MethodGet, "/v1/agents", f.token, nil)
			var defaults core.AgentsResult
			if status != http.StatusOK || json.Unmarshal(data, &defaults) != nil || defaults.Version != 1 || !reflect.DeepEqual(defaults.Agents, want) {
				t.Fatalf("default agents bound status=%d body=%s", status, data)
			}
			status, _, data = httpCall(t, f, http.MethodGet, "/v1/agents?limit=1", f.token, nil)
			var capacity map[string]json.RawMessage
			if status != http.StatusTooManyRequests || json.Unmarshal(data, &capacity) != nil || capacity["agents"] != nil {
				t.Fatalf("overflow exposed partial agents status=%d body=%s", status, data)
			}
			for _, encrypted := range []bool{false, true} {
				request := core.Request{Version: 1, RequestID: fmt.Sprintf("agents-detached-%t", encrypted), Operation: core.AgentsRead, Limit: core.MaxAgents}
				status, _, result := detachedCall(t, f, f.credentials.token(t, request, encrypted), encrypted)
				if status != http.StatusOK || result.Agents == nil || result.Agents.Version != 1 {
					t.Fatalf("agents detached encrypted=%t status=%d result=%+v", encrypted, status, result)
				}
				assertAgentsResult(t, result.Agents.Agents, want)
			}
			if _, err := f.journal.Lookup(t.Context(), f.identity, "agents"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("agents read created job: %v", err)
			}
			for _, test := range []struct {
				path  string
				token string
				code  int
			}{
				{"/v1/agents?limit=0", f.token, http.StatusBadRequest},
				{"/v1/agents?limit=257", f.token, http.StatusBadRequest},
				{"/v1/agents?limit=2&limit=3", f.token, http.StatusBadRequest},
				{"/v1/agents?target=%23gateway-fixture", f.token, http.StatusBadRequest},
				{"/v1/agents?limit=256", f.other, http.StatusForbidden},
			} {
				status, _, data := httpCall(t, f, http.MethodGet, test.path, test.token, nil)
				if status != test.code || len(data) == 0 || string(data) == "null" {
					t.Fatalf("agents denial=%+v status=%d body=%s", test, status, data)
				}
			}
			_ = member.Close()
			waitNativeAgentAbsent(t, f.socket, "agents-member")
			status, got = agentsBearerHTTP(t, f, "256")
			if status != http.StatusOK {
				t.Fatal(status)
			}
			assertAgentsResult(t, got, nativeAgents(t, f.socket))
			if !reflect.DeepEqual(before, nativeWebTransportMessages(t, f.socket, "#gateway-fixture")) {
				t.Fatal("agent discovery changed native history")
			}
			if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
				t.Fatal(err)
			}
			status, _, _ = httpCall(t, f, http.MethodGet, "/v1/agents?limit=256", f.token, nil)
			if status != http.StatusForbidden {
				t.Fatalf("agents read bypassed revocation: %d", status)
			}
		})
	}
}

func TestAgentsReadFramedAndWebSocketMembership(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	member := joinAgentsMember(t, f.socket)
	waitNativeAgent(t, f.socket, "agents-member")
	want := nativeAgents(t, f.socket)
	request := core.Request{Version: 1, RequestID: "agents-frame-encrypted", Operation: core.AgentsRead, Limit: core.MaxAgents}
	token := f.cred.token(t, request, true)
	status, framed := contextFramedCall(t, f.framed, token, request.RequestID+"-framed", true)
	if status != http.StatusOK || framed.Agents == nil {
		t.Fatalf("framed agents status=%d result=%+v", status, framed)
	}
	assertAgentsResult(t, framed.Agents.Agents, want)
	status, socket := contextWebSocketCall(t, f.ws, token, request.RequestID+"-websocket", true)
	if status != http.StatusOK || socket.Agents == nil {
		t.Fatalf("websocket agents status=%d result=%+v", status, socket)
	}
	assertAgentsResult(t, socket.Agents.Agents, want)
	_ = member.Close()
}
