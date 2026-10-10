//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestAgentsReadUnixAndTLSFramed(t *testing.T) {
	t.Run("unix", func(t *testing.T) {
		conn, credentials, stop, _, socket := unixStreamFixture(t)
		defer conn.Close()
		defer stop()
		joinAgentsMember(t, socket)
		want := nativeAgents(t, socket)
		request := agentsRequest("agents-unix")
		status, result := contextFramedCall(t, conn, credentials.token(t, request, true), request.RequestID, true)
		if status != http.StatusOK || result.Agents == nil || result.Agents.Version != 1 {
			t.Fatalf("Unix agents status=%d result=%+v", status, result)
		}
		assertAgentsResult(t, result.Agents.Agents, want)
	})
	t.Run("tls", func(t *testing.T) {
		conn, credentials, _, stop := framedFixture(t, true)
		defer conn.Close()
		defer stop()
		request := agentsRequest("agents-tls")
		status, result := contextFramedCall(t, conn, credentials.token(t, request, false), request.RequestID, false)
		// This isolated fixture has only ephemeral sessions and no visible peers.
		if status != http.StatusOK || result.Agents == nil || result.Agents.Version != 1 || result.Agents.Agents == nil || len(result.Agents.Agents) != 0 {
			t.Fatalf("TLS framed empty agents status=%d result=%+v", status, result)
		}
	})
	t.Run("wss", func(t *testing.T) {
		f := websocketGatewayFixture(t, true, "127.0.0.1:0", nil)
		defer f.conn.CloseNow()
		defer f.stop()
		joinAgentsMember(t, f.socket)
		want := nativeAgents(t, f.socket)
		request := agentsRequest("agents-wss")
		status, result := contextWebSocketCall(t, f.conn, f.credentials.token(t, request, false), request.RequestID, false)
		if status != http.StatusOK || result.Agents == nil || result.Agents.Version != 1 {
			t.Fatalf("WSS agents status=%d result=%+v", status, result)
		}
		assertAgentsResult(t, result.Agents.Agents, want)
	})
}

func TestAgentsReadJSONRPCNativeSnapshot(t *testing.T) {
	f := newRPCHTTPFixture(t, true)
	joinAgentsMember(t, f.config.Bindings[0].Address)
	want := nativeAgents(t, f.config.Bindings[0].Address)
	for _, h2 := range []bool{false, true} {
		request := agentsRequest("agents-rpc")
		response, data := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request)), h2)
		rpc := decodeRPC(t, data)
		var result core.AgentsResult
		proto := 1
		if h2 {
			proto = 2
		}
		if response.StatusCode != http.StatusOK || response.ProtoMajor != proto || len(rpc.Error) != 0 || json.Unmarshal(rpc.Result, &result) != nil || result.Version != 1 {
			t.Fatalf("JSON-RPC agents status=%d proto=%d body=%s", response.StatusCode, response.ProtoMajor, data)
		}
		assertAgentsResult(t, result.Agents, want)
	}
}
