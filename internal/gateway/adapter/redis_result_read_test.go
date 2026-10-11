//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRedisResultsPreserveWholePageAndContext(t *testing.T) {
	message := irc.MessageMetadata{ID: "message-id", From: "author", Target: "#fixture", Message: "exact Unicode 世界\nsecond line", Timestamp: time.Unix(1000, 0).UTC()}
	for name, encode := range redisResultEncoders {
		for _, result := range []dispatch.DetachedResult{
			{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-resume-token", Status: "more", Gap: true}},
			{Version: 1, Operation: core.Search, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-search-token", Status: "ok", Gap: false}},
			{Version: 1, Operation: core.ContextRead, Context: &core.ContextResult{Summary: irc.ContextSummary{RootID: "root", TriggerID: "trigger", Missing: []string{"missing"}, OmittedMessages: 2}, Messages: []irc.MessageMetadata{message}}},
		} {
			wire, err := encode("read-id", &result, nil)
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				RequestID string                   `json:"request_id"`
				Result    *dispatch.DetachedResult `json:"result"`
			}
			if err := json.Unmarshal(wire, &response); err != nil {
				t.Fatal(err)
			}
			if response.RequestID != "read-id" || response.Result == nil || !reflect.DeepEqual(*response.Result, result) {
				t.Fatalf("%s: read boundary changed for %s", name, result.Operation)
			}
		}
	}
}

// A read whose parent deadline passes while aircd stalls answers a
// correlated transient failure promptly and closes its native connection.
func TestRedisAdmissionParentDeadlineClosesNativeRead(t *testing.T) {
	for _, carrier := range []string{"pubsub", "streams"} {
		for _, op := range []core.Operation{core.ContextRead, core.HistoryRead, core.Search} {
			t.Run(carrier+"/"+string(op), func(t *testing.T) {
				f := newRedisFixture(t)
				assertNativeClosed := stalledNativeRead(t, f.c.Upstream.Address)
				request := core.Request{Version: 1, RequestID: "redis-read", Operation: op, Target: redisRequest().Target, Limit: 1}
				if op == core.ContextRead {
					request.ParentID = strings.Repeat("a", 32)
				}
				if op == core.Search {
					request.Query = "exact"
				}
				ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
				defer cancel()
				started := time.Now()
				slots := make(chan struct{}, 1)
				var a redisAdmission
				if carrier == "pubsub" {
					a = admitRedis(ctx, f.d, f.author, "route:redis", redisAdmissionRoute(), redisAdmissionRoute().RequestChannel, redisPlainRequest(t, request), slots)
				} else {
					broker := redisStreamBroker()
					a = admitRedisStream(ctx, f.d, f.author, broker, broker.Route.Stream, redisStreamEntry(t, request), slots)
				}
				if a.refused || a.result != nil || a.failure == nil || !a.failure.Transient || a.requestID != request.RequestID || time.Since(started) > time.Second || len(slots) != 0 {
					t.Fatalf("bounded read attempt: %+v", a)
				}
				assertNativeClosed()
				if _, err := f.lookup(t, request.RequestID); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("read admitted mutation", err)
				}
			})
		}
	}
}
