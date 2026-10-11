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

// redisResultEncoders build a request's result as Redis Pub/Sub and Redis
// Streams publish it.
var redisResultEncoders = map[string]func(id string, result *dispatch.DetachedResult, failure *core.Failure) ([]byte, error){
	"pubsub": func(id string, result *dispatch.DetachedResult, failure *core.Failure) ([]byte, error) {
		return redisResultMessage(redisResultRoute(), id, result, failure)
	},
	"streams": func(id string, result *dispatch.DetachedResult, failure *core.Failure) ([]byte, error) {
		return redisStreamResultMessage(redisStreamResultBroker(), "1-0", id, result, failure)
	},
}

func TestRedisResultShapeRefusals(t *testing.T) {
	valid := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "id", Durable: true}}
	for name, encode := range redisResultEncoders {
		for _, result := range []*dispatch.DetachedResult{
			nil, {}, {Version: 1, Operation: core.Send}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: "other", Durable: true}}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: "id"}}, {Version: 1, Operation: core.ContextRead}, {Version: 1, Operation: core.HistoryRead}, {Version: 1, Operation: core.Search}, {Version: 1, Operation: core.ReceiptGet, Result: valid.Result},
		} {
			if _, err := encode("id", result, nil); !errors.Is(err, rediswire.ErrFrame) {
				t.Fatal(name, "shape accepted", err)
			}
		}
		if _, err := encode("id", &valid, &core.Failure{}); !errors.Is(err, rediswire.ErrFrame) {
			t.Fatal(name, "both result and failure", err)
		}
		for _, id := range []string{"", strings.Repeat("a", 65)} {
			if _, err := encode(id, nil, &core.Failure{Code: core.InvalidRequest}); !errors.Is(err, rediswire.ErrFrame) {
				t.Fatal(name, "invalid ID", err)
			}
		}
	}
	other := redisResultRoute()
	other.ResponseChannel = other.RequestChannel
	if _, err := redisResultMessage(other, "id", nil, &core.Failure{}); !errors.Is(err, rediswire.ErrFrame) {
		t.Fatal("aliased channels", err)
	}
}

func TestRedisResultCapacityDoesNotTruncateCursor(t *testing.T) {
	for name, encode := range redisResultEncoders {
		result := dispatch.DetachedResult{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Cursor: strings.Repeat("c", rediswire.MaxBulk), Status: "more", Gap: true}}
		if b, err := encode("id", &result, nil); !errors.Is(err, errRedisResultCapacity) || b != nil {
			t.Fatal(name, "oversize page emitted or changed", err)
		}
		if len(result.Page.Cursor) != rediswire.MaxBulk || result.Page.Status != "more" || !result.Page.Gap {
			t.Fatal(name, "page/cursor truncated")
		}
		wire, err := encode("id", nil, &core.Failure{Code: core.CapacityExceeded, Transient: true})
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Error *core.Failure `json:"error"`
		}
		if len(wire) > rediswire.MaxBulk || json.Unmarshal(wire, &response) != nil || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient {
			t.Fatal(name, "fallback failure")
		}
	}
}
