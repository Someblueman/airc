package adapter

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestKafkaWholeReadReplayFreshness(t *testing.T) {
	sealer, pub, route, _ := redisResultFixture(t)
	b := config.Kafka{Namespace: "history", Route: config.KafkaRoute{Principal: route.Principal, Topic: "events", Partition: 2, ResultTopic: "results", ResultPartition: 3, Target: route.Target, Operations: []core.Operation{core.HistoryRead, core.Search, core.ContextRead}}}
	message := irc.MessageMetadata{ID: "message-id", From: "author", Target: route.Target.Name, Message: "exact Unicode 世界\nsecond line", Timestamp: time.Unix(1000, 0).UTC()}
	for _, result := range []dispatch.DetachedResult{
		{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-resume-token", Status: "more", Gap: true}},
		{Version: 1, Operation: core.Search, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-search-token", Status: "ok"}},
		{Version: 1, Operation: core.ContextRead, Context: &core.ContextResult{Summary: irc.ContextSummary{RootID: "root", TriggerID: "trigger", Missing: []string{"missing"}, OmittedMessages: 2}, Messages: []irc.MessageMetadata{message}}},
	} {
		o := kafkaOutcome{Version: 1, RequestID: "original-id", Operation: result.Operation, Result: &result}
		cache, err := encodeKafkaOutcome(o)
		if err != nil {
			t.Fatal(err)
		}
		retained, err := decodeKafkaOutcome(cache, o.RequestID, o.Operation)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		firstScope := kafkaResponseScope("realm", route.Principal, "binding", b, 23, o.RequestID)
		first, err := kafkaResultMessage(sealer, firstScope, b, 23, retained, now)
		if err != nil {
			t.Fatal(err)
		}
		later := now.Add(auth.ResponseLifetime + time.Duration(auth.ClockSkewSeconds+1)*time.Second)
		if _, err := auth.OpenSignedResponse(string(first), "response", pub, firstScope, later); err == nil {
			t.Fatal("expired JWS accepted")
		}
		// A different broker record of the same intent gets fresh response scope,
		// without changing the retained page, metadata or opaque cursor.
		scope := kafkaResponseScope("realm", route.Principal, "binding", b, 24, o.RequestID)
		replay, err := kafkaResultMessage(sealer, scope, b, 24, retained, later)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := auth.OpenSignedResponse(string(replay), "response", pub, scope, later)
		if err != nil {
			t.Fatal(err)
		}
		var got KafkaResult
		if err := json.Unmarshal(plain, &got); err != nil {
			t.Fatal(err)
		}
		if got.Offset != 24 || got.RequestID != o.RequestID || !reflect.DeepEqual(got.Result, &result) {
			t.Fatal("whole read changed on replay", got)
		}
		after, err := encodeKafkaOutcome(retained)
		if err != nil || string(after) != string(cache) {
			t.Fatal("cache changed", err)
		}
	}
}
