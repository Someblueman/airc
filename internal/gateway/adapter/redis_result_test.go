package adapter

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func redisResultRoute() config.RedisRoute {
	return config.RedisRoute{Principal: "principal", RequestChannel: "airc.request", ResponseChannel: "airc.response", Target: core.Target{Kind: "room", Name: "#fixture"}}
}

func TestRedisResultIsPlainJSONWithCorrelation(t *testing.T) {
	route := redisResultRoute()
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "stable-id", State: core.Queued, Durable: true}}
	wire, err := redisResultMessage(route, "stable-id", &result, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A client needs only a JSON parser: no envelope, signature or key.
	var client struct {
		Version         int    `json:"version"`
		Protocol        string `json:"protocol"`
		RequestID       string `json:"request_id"`
		RequestChannel  string `json:"request_channel"`
		ResponseChannel string `json:"response_channel"`
		Target          struct{ Kind, Name string }
		Result          struct {
			Operation string `json:"operation"`
			Result    struct {
				RequestID string `json:"request_id"`
				Durable   bool   `json:"durable"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(wire, &client); err != nil {
		t.Fatalf("result is not plain JSON: %s: %v", wire, err)
	}
	if client.Version != 1 || client.Protocol != "redis/pubsub" || client.RequestID != "stable-id" || client.RequestChannel != route.RequestChannel || client.ResponseChannel != route.ResponseChannel || client.Target.Kind != "room" || client.Target.Name != "#fixture" || client.Result.Operation != "send" || client.Result.Result.RequestID != "stable-id" || !client.Result.Result.Durable {
		t.Fatalf("client view: %s", wire)
	}
	var response RedisResult
	if err := json.Unmarshal(wire, &response); err != nil || response.Error != nil || response.Result == nil || response.Result.Result.Accepted != nil {
		t.Fatal("correlation or acceptance changed", err)
	}
	wire, err = redisResultMessage(route, "failed-id", nil, &core.Failure{Code: core.IDConflict})
	if err != nil {
		t.Fatal(err)
	}
	response = RedisResult{}
	if err := json.Unmarshal(wire, &response); err != nil || response.RequestID != "failed-id" || response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatal("failure correlation", err)
	}
}
