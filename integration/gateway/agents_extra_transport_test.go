//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func agentsRequest(id string) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: core.AgentsRead, Limit: core.MaxAgents}
}

func TestAgentsReadGRPCExactNativeSnapshot(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	member := joinAgentsMember(t, f.config.Upstream.Address)
	defer member.Close()
	want := sortAgents(nativeAgents(t, f.config.Upstream.Address))
	var got core.AgentsResult
	data := grpcReadJSON(t, f, agentsRequest("agents-grpc"))
	if err := json.Unmarshal(data, &got); err != nil || got.Version != 1 {
		t.Fatalf("gRPC agents payload=%s err=%v", data, err)
	}
	assertAgentsResult(t, sortAgents(got.Agents), want)
}

func TestAgentsReadStdioExactNativeSnapshot(t *testing.T) {
	socket, c, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	p := startStdio(t, gatewayBinary(t), c)
	want := sortAgents(nativeAgents(t, socket))
	for _, encrypted := range []bool{false, true} {
		id := fmt.Sprintf("agents-stdio-%t", encrypted)
		request := agentsRequest(id)
		p.write(t, frameRequest{Version: 1, CorrelationID: id, Profile: "plain", Request: credentials.token(t, request, encrypted)})
		response := p.read(t)
		if response.Version != 1 || response.CorrelationID != id || response.Event != "result" || response.Error != nil || response.Result == nil || response.Result.Agents == nil || response.Result.Agents.Version != 1 {
			t.Fatalf("stdio agents encrypted=%t response=%+v", encrypted, response)
		}
		assertAgentsResult(t, sortAgents(response.Result.Agents.Agents), want)
	}
	if err := p.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(); err != nil {
		t.Fatalf("stdio agents process did not join: %v stderr=%s", err, p.stderr.String())
	}
}
