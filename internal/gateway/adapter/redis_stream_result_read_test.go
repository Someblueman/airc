package adapter

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRedisStreamResultsPreserveWholePageAndContext(t *testing.T) {
	broker, id := redisStreamResultBroker(), "read-id"
	message := irc.MessageMetadata{ID: "message-id", From: "author", Target: broker.Route.Target.Name, Message: "exact Unicode 世界\nsecond line", Timestamp: time.Unix(1000, 0).UTC()}
	for _, result := range []dispatch.DetachedResult{
		{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-resume-token", Status: "more", Gap: true}},
		{Version: 1, Operation: core.Search, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-search-token", Status: "ok", Gap: false}},
		{Version: 1, Operation: core.ContextRead, Context: &core.ContextResult{Summary: irc.ContextSummary{RootID: "root", TriggerID: "trigger", Missing: []string{"missing"}, OmittedMessages: 2}, Messages: []irc.MessageMetadata{message}}},
	} {
		wire, err := redisStreamResultMessage(broker, "1-0", id, &result, nil)
		if err != nil {
			t.Fatal(err)
		}
		var response RedisStreamResult
		if err := json.Unmarshal(wire, &response); err != nil {
			t.Fatal(err)
		}
		if response.RequestID != id || response.Result == nil || !reflect.DeepEqual(*response.Result, result) {
			t.Fatalf("read boundary changed for %s", result.Operation)
		}
	}
}
