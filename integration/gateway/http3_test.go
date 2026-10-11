//go:build darwin || linux

package gateway_test

import (
	"bytes"
	cryptotls "crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func http3Call(t *testing.T, f http3GatewayFixture, token string, path string, body []byte) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.address+path, bytesReader(body))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, token)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	assertHTTP3Security(t, response)
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func bytesReader(body []byte) *bytes.Reader { return bytes.NewReader(body) }

func assertHTTP3Security(t *testing.T, response *http.Response) {
	t.Helper()
	if response.ProtoMajor != 3 || response.TLS == nil || response.TLS.Version != cryptotls.VersionTLS13 || response.TLS.NegotiatedProtocol != "h3" {
		t.Fatalf("HTTP/3 security=%d/%v tls=%v", response.ProtoMajor, response.TLS, response.TLS)
	}
}

func TestHTTP3CapabilitiesAndReadMatrix(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	response, data := http3Call(t, f, f.token, "/v1/rpc", rpcRequestBody(t, "http3-cap", "airc.capabilities", map[string]any{}))
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 {
		t.Fatalf("HTTP/3 capabilities status/protocol=%d/%d body=%s", response.StatusCode, response.ProtoMajor, data)
	}
	capabilities := decodeRPC(t, data)
	var got struct {
		Operations []core.Operation `json:"operations"`
	}
	if err := json.Unmarshal(capabilities.Result, &got); err != nil || !reflect.DeepEqual(got.Operations, dispatch.ImplementedOperations()) {
		t.Fatalf("HTTP/3 capabilities=%s", capabilities.Result)
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parentID := seedPinMutationMessage(t, f.config.Upstream.Address, room.Name, "http3-parent")
	nativeParent := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == parentID })
	reads := []core.Request{
		{Version: 1, RequestID: "http3-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16},
		{Version: 1, RequestID: "http3-search", Operation: core.Search, Target: room, Query: "pin mutation", Limit: 16},
		{Version: 1, RequestID: "http3-context", Operation: core.ContextRead, Target: room, ParentID: parentID, Limit: 16},
		{Version: 1, RequestID: "http3-pins", Operation: core.PinsRead, Target: room},
		{Version: 1, RequestID: "http3-room", Operation: core.RoomRead, Target: room},
		{Version: 1, RequestID: "http3-waiting", Operation: core.WaitingRead, Target: room, ParentID: parentID},
		{Version: 1, RequestID: "http3-topic", Operation: core.TopicRead, Target: room},
		{Version: 1, RequestID: "http3-members", Operation: core.MembersRead, Target: room},
		{Version: 1, RequestID: "http3-channels", Operation: core.ChannelsRead},
		{Version: 1, RequestID: "http3-directory", Operation: core.Directory, Query: "fixture-author", Limit: 16},
		{Version: 1, RequestID: "http3-events", Operation: core.EventsWatch, Target: room, Start: "retained", Limit: 16},
	}
	for _, request := range reads {
		response, data := http3Call(t, f, f.token, "/v1/rpc", rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request)))
		if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 || len(decodeRPC(t, data).Result) < 3 {
			t.Fatalf("HTTP/3 read %s status/protocol/body=%d/%d/%s", request.Operation, response.StatusCode, response.ProtoMajor, data)
		}
		result := decodeRPC(t, data).Result
		switch request.Operation {
		case core.HistoryRead, core.Search, core.ContextRead, core.EventsWatch:
			var page backend.HistoryPage
			if err := json.Unmarshal(result, &page); err != nil || len(page.Messages) == 0 || page.Messages[0].ID != parentID || page.Messages[0].Message != nativeParent.Message || page.Messages[0].Target != nativeParent.Target || page.Messages[0].From != nativeParent.From {
				t.Fatalf("HTTP/3 %s lost native whole message: result=%s page=%+v native=%+v err=%v", request.Operation, result, page, nativeParent, err)
			}
		case core.PinsRead:
			var pins core.PinsResult
			if err := json.Unmarshal(result, &pins); err != nil || pins.Target != *room || pins.Pins == nil {
				t.Fatalf("HTTP/3 pins snapshot=%s err=%v", result, err)
			}
		case core.RoomRead:
			var roomResult core.RoomResult
			if err := json.Unmarshal(result, &roomResult); err != nil || roomResult.Target != *room {
				t.Fatalf("HTTP/3 room snapshot=%s err=%v", result, err)
			}
		case core.WaitingRead:
			var waiting core.WaitingResult
			if err := json.Unmarshal(result, &waiting); err != nil || waiting.Target != *room || waiting.ParentID != parentID || waiting.Signals == nil {
				t.Fatalf("HTTP/3 waiting snapshot=%s err=%v", result, err)
			}
		case core.TopicRead:
			var topic core.TopicResult
			if err := json.Unmarshal(result, &topic); err != nil || topic.Target != *room {
				t.Fatalf("HTTP/3 topic snapshot=%s err=%v", result, err)
			}
		case core.MembersRead:
			var members core.MembersResult
			if err := json.Unmarshal(result, &members); err != nil || members.Target != *room || members.Members == nil {
				t.Fatalf("HTTP/3 members snapshot=%s err=%v", result, err)
			}
		case core.ChannelsRead:
			var channels struct {
				Channels []core.Channel `json:"channels"`
			}
			if err := json.Unmarshal(result, &channels); err != nil || channels.Channels == nil {
				t.Fatalf("HTTP/3 channels snapshot=%s err=%v", result, err)
			}
		case core.Directory:
			var directory backend.DirectorySnapshot
			if err := json.Unmarshal(result, &directory); err != nil || len(directory.Agents) == 0 || directory.Agents[0].Nick != "fixture-author" {
				t.Fatalf("HTTP/3 directory snapshot=%s err=%v", result, err)
			}
		}
	}
}
