package adapter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisResultShapeRefusals(t *testing.T) {
	route := redisResultRoute()
	valid := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "id", Durable: true}}
	for _, result := range []*dispatch.DetachedResult{
		nil, {}, {Version: 1, Operation: core.Send}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: "other", Durable: true}}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: "id"}}, {Version: 1, Operation: core.ContextRead}, {Version: 1, Operation: core.HistoryRead}, {Version: 1, Operation: core.Search}, {Version: 1, Operation: core.ReceiptGet, Result: valid.Result},
	} {
		if _, err := redisResultMessage(route, "id", result, nil); !errors.Is(err, rediswire.ErrFrame) {
			t.Fatal("shape accepted", err)
		}
	}
	if _, err := redisResultMessage(route, "id", &valid, &core.Failure{}); !errors.Is(err, rediswire.ErrFrame) {
		t.Fatal("both result and failure", err)
	}
	for _, id := range []string{"", strings.Repeat("a", 65)} {
		if _, err := redisResultMessage(route, id, nil, &core.Failure{Code: core.InvalidRequest}); !errors.Is(err, rediswire.ErrFrame) {
			t.Fatal("invalid ID", err)
		}
	}
	other := route
	other.ResponseChannel = other.RequestChannel
	if _, err := redisResultMessage(other, "id", nil, &core.Failure{}); !errors.Is(err, rediswire.ErrFrame) {
		t.Fatal("aliased channels", err)
	}
}

func TestRedisResultCapacityDoesNotTruncateCursor(t *testing.T) {
	route := redisResultRoute()
	result := dispatch.DetachedResult{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Cursor: strings.Repeat("c", rediswire.MaxBulk), Status: "more", Gap: true}}
	if b, err := redisResultMessage(route, "id", &result, nil); !errors.Is(err, errRedisResultCapacity) || b != nil {
		t.Fatal("oversize page emitted or changed", err)
	}
	if len(result.Page.Cursor) != rediswire.MaxBulk || result.Page.Status != "more" || !result.Page.Gap {
		t.Fatal("page/cursor truncated")
	}
	wire, err := redisResultMessage(route, "id", nil, &core.Failure{Code: core.CapacityExceeded, Transient: true})
	if err != nil {
		t.Fatal(err)
	}
	var response RedisResult
	if len(wire) > rediswire.MaxBulk || json.Unmarshal(wire, &response) != nil || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient {
		t.Fatal("fallback failure")
	}
}
