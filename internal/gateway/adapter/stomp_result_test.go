package adapter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPPlainResultNamesDestinationsAndInnerID(t *testing.T) {
	route := config.STOMPRoute{RequestDestination: "requests/a:send", ResponseDestination: "responses/a:read"}
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "stable-inner-id", State: core.Queued, Durable: true}}
	wire, err := stompResultMessage(route, "stable-inner-id", &result, nil)
	if err != nil {
		t.Fatal(err)
	}
	var response STOMPResult
	if err := json.Unmarshal(wire, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 1 || response.Protocol != "stomp/1.2" || response.RequestID != "stable-inner-id" || response.RequestDestination != route.RequestDestination || response.ResponseDestination != route.ResponseDestination || response.Result == nil || !response.Result.Result.Durable || response.Error != nil {
		t.Fatal(response)
	}
	wire, err = stompResultMessage(route, "failed-id", nil, &core.Failure{Code: core.IDConflict})
	if err != nil {
		t.Fatal(err)
	}
	response = STOMPResult{}
	if err := json.Unmarshal(wire, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "failed-id" || response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatal(response)
	}
}

func TestSTOMPPlainResultCapacityAndShape(t *testing.T) {
	route := config.STOMPRoute{RequestDestination: "request", ResponseDestination: "response"}
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: strings.Repeat("x", stompwire.MaxBody)}}
	if _, err := stompResultMessage(route, "id", &result, nil); !errors.Is(err, errResultCapacity) {
		t.Fatal("oversized result", err)
	}
	if _, err := stompResultMessage(route, "", &result, nil); !errors.Is(err, stompwire.ErrFrame) {
		t.Fatal("empty request ID", err)
	}
	if _, err := stompResultMessage(route, "id", nil, nil); !errors.Is(err, stompwire.ErrFrame) {
		t.Fatal(err)
	}
	if _, err := stompResultMessage(route, "id", &result, &core.Failure{}); !errors.Is(err, stompwire.ErrFrame) {
		t.Fatal(err)
	}
}
