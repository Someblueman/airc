package adapter

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisStreamResultShapeAndScopeRefusals(t *testing.T) {
	sealer, _, broker, scope, id := redisStreamResultFixture(t)
	now := time.Now()
	valid := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: id, Durable: true}}
	for _, result := range []*dispatch.DetachedResult{
		nil, {}, {Version: 1, Operation: core.Send}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: "other", Durable: true}}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: id}}, {Version: 1, Operation: core.ContextRead}, {Version: 1, Operation: core.HistoryRead}, {Version: 1, Operation: core.Search}, {Version: 1, Operation: core.ReceiptGet, Result: valid.Result},
	} {
		if _, err := redisStreamResultMessage(sealer, scope, broker, "1-0", id, result, nil, now); !errors.Is(err, rediswire.ErrFrame) {
			t.Fatal("shape accepted", err)
		}
	}
	if _, err := redisStreamResultMessage(sealer, scope, broker, "1-0", id, &valid, &core.Failure{}, now); !errors.Is(err, rediswire.ErrFrame) {
		t.Fatal("both result and failure", err)
	}
	for _, badID := range []string{"", strings.Repeat("a", 65)} {
		if _, err := redisStreamResultMessage(sealer, scope, broker, "1-0", badID, nil, &core.Failure{Code: core.InvalidRequest}, now); !errors.Is(err, rediswire.ErrFrame) {
			t.Fatal("invalid ID", err)
		}
	}
	other := broker
	other.Route.ResultStream = "other.response"
	if _, err := redisStreamResultMessage(sealer, scope, other, "1-0", id, nil, &core.Failure{}, now); !errors.Is(err, rediswire.ErrFrame) {
		t.Fatal("scope/route mismatch", err)
	}
	other = broker
	other.Route.Principal = "other"
	if _, err := redisStreamResultMessage(sealer, scope, other, "1-0", id, nil, &core.Failure{}, now); !errors.Is(err, rediswire.ErrFrame) {
		t.Fatal("principal/route mismatch", err)
	}
	if _, err := redisStreamResultMessage(nil, scope, broker, "1-0", id, &valid, nil, now); !errors.Is(err, auth.ErrCredential) {
		t.Fatal("missing sealer", err)
	}
}

func TestRedisStreamResultCapacityDoesNotTruncateCursor(t *testing.T) {
	sealer, pub, broker, scope, id := redisStreamResultFixture(t)
	now := time.Now()
	result := dispatch.DetachedResult{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Cursor: strings.Repeat("c", 25000), Status: "more", Gap: true}}
	if b, err := redisStreamResultMessage(sealer, scope, broker, "1-0", id, &result, nil, now); !errors.Is(err, auth.ErrResponseCapacity) || b != nil {
		t.Fatal("oversize page emitted or changed", err)
	}
	if len(result.Page.Cursor) != 25000 || result.Page.Status != "more" || !result.Page.Gap {
		t.Fatal("page/cursor truncated")
	}
	wire, err := redisStreamResultMessage(sealer, scope, broker, "1-0", id, nil, &core.Failure{Code: core.CapacityExceeded, Transient: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) > rediswire.MaxBulk {
		t.Fatal("fallback failure exceeds profile bound")
	}
	if _, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now); err != nil {
		t.Fatal(err)
	}
}
