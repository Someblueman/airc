//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestAgentsReadHTTP3AndWebTransport(t *testing.T) {
	t.Run("http3", func(t *testing.T) {
		f := newHTTP3GatewayFixture(t)
		joinAgentsMember(t, f.config.Upstream.Address)
		want := nativeAgents(t, f.config.Upstream.Address)
		request := core.Request{Version: 1, RequestID: "agents-http3", Operation: core.AgentsRead, Limit: core.MaxAgents}
		response, data := http3Call(t, f, f.token, "/v1/rpc", rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request)))
		var result core.AgentsResult
		rpc := decodeRPC(t, data)
		if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 || len(rpc.Error) != 0 || json.Unmarshal(rpc.Result, &result) != nil || result.Version != 1 || !reflect.DeepEqual(result.Agents, want) {
			t.Fatalf("HTTP/3 agents status=%d proto=%d body=%s want=%+v", response.StatusCode, response.ProtoMajor, data, want)
		}
	})
	t.Run("webtransport", func(t *testing.T) {
		f := newWebTransportFixture(t)
		joinAgentsMember(t, f.config.Upstream.Address)
		want := nativeAgents(t, f.config.Upstream.Address)
		for _, encrypted := range []bool{false, true} {
			request := core.Request{Version: 1, RequestID: "agents-webtransport", Operation: core.AgentsRead, Limit: core.MaxAgents}
			result := webTransportBidiCall(t, f, request, encrypted)
			if result.Agents == nil || result.Agents.Version != 1 || !reflect.DeepEqual(result.Agents.Agents, want) {
				t.Fatalf("WebTransport agents encrypted=%t result=%+v want=%+v", encrypted, result.Agents, want)
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		request := core.Request{Version: 1, RequestID: "agents-webtransport-uni", Operation: core.AgentsRead, Limit: core.MaxAgents}
		frame := adapterFrameRequest(t, f, request, true)
		out, err := f.session.OpenUniStreamSync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(webTransportFrame(t, frame)); err != nil {
			t.Fatal(err)
		}
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
		in, err := f.session.AcceptUniStream(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer in.CancelRead(0)
		response := readWebTransportFrame(t, in)
		if response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Result == nil || response.Result.Agents == nil || !reflect.DeepEqual(response.Result.Agents.Agents, want) {
			t.Fatalf("WebTransport uni agents=%+v want=%+v", response, want)
		}
		request.RequestID = "agents-webtransport-datagram"
		frame = adapterFrameRequest(t, f, request, true)
		var id [16]byte
		copy(id[:], "agents-datagram")
		for _, fragment := range webTransportDatagrams(webTransportFrame(t, frame)[4:], id) {
			if err := f.session.SendDatagram(fragment); err != nil {
				t.Fatal(err)
			}
		}
		response = receiveWebTransportDatagramFrame(t, f, ctx)
		if response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Agents == nil || !reflect.DeepEqual(response.Result.Agents.Agents, want) {
			t.Fatalf("WebTransport datagram agents=%+v want=%+v", response, want)
		}
	})
}
