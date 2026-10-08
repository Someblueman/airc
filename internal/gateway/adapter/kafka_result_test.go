package adapter

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

func kafkaSendOutcome() kafkaOutcome {
	return kafkaOutcome{Version: 1, RequestID: "producer-id", Operation: core.Send, Result: &dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "producer-id", Durable: true, State: core.Queued}}}
}

func TestKafkaOutcomeCanonicalCache(t *testing.T) {
	for _, o := range []kafkaOutcome{
		kafkaSendOutcome(),
		{Version: 1, RequestID: "producer-id", Operation: core.HistoryRead, Result: &dispatch.DetachedResult{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Cursor: "whole-cursor"}}},
		{Version: 1, RequestID: "producer-id", Operation: core.Search, Error: &core.Failure{Code: core.UpstreamUnavailable, Transient: true, UpstreamCode: 503}},
		{Version: 1, RequestID: "producer-id", Operation: core.ContextRead, Result: &dispatch.DetachedResult{Version: 1, Operation: core.ContextRead, Context: &core.ContextResult{}}},
	} {
		data, err := encodeKafkaOutcome(o)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeKafkaOutcome(data, o.RequestID, o.Operation)
		if err != nil || !reflect.DeepEqual(got, o) {
			t.Fatal("whole cache round trip", got, err)
		}
		for _, bad := range [][]byte{
			append([]byte(" "), data...),
			[]byte(strings.Replace(string(data), `"version":1`, `"Version":1`, 1)),
			[]byte(strings.Replace(string(data), `"version":1`, `"version":1,"version":1`, 1)),
			[]byte(strings.Replace(string(data), `"version":1`, `"extra":true,"version":1`, 1)),
			append(data, []byte("null")...),
		} {
			if _, err := decodeKafkaOutcome(bad, o.RequestID, o.Operation); !errors.Is(err, kafkawire.ErrFrame) {
				t.Fatal("bad cache accepted", string(bad), err)
			}
		}
		if _, err := decodeKafkaOutcome(data, "other", o.Operation); err == nil {
			t.Fatal("wrong intent accepted")
		}
		if _, err := decodeKafkaOutcome(data, o.RequestID, core.Watch); err == nil {
			t.Fatal("wrong operation accepted")
		}
	}
	for _, mutate := range []func(*kafkaOutcome){
		func(o *kafkaOutcome) { o.Version = 2 },
		func(o *kafkaOutcome) { o.Error = &core.Failure{Code: core.Forbidden} },
		func(o *kafkaOutcome) { o.Result.Page = &backend.HistoryPage{} },
		func(o *kafkaOutcome) { o.Result.Result.Durable = false },
		func(o *kafkaOutcome) { o.Result.Result.RequestID = "other" },
		func(o *kafkaOutcome) { o.Result.Operation = core.Search },
		func(o *kafkaOutcome) { o.RequestID = "bad.id" },
		func(o *kafkaOutcome) { o.Result = nil; o.Error = &core.Failure{Code: "invented"} },
	} {
		o := kafkaSendOutcome()
		mutate(&o)
		if _, err := encodeKafkaOutcome(o); !errors.Is(err, kafkawire.ErrFrame) {
			t.Fatal("invalid shape accepted", err)
		}
	}
}

func TestKafkaSignedWholeResult(t *testing.T) {
	sealer, pub, r, _ := redisResultFixture(t)
	b := config.Kafka{Namespace: "history", Route: config.KafkaRoute{Principal: r.Principal, Topic: "events", Partition: 2, ResultTopic: "results", ResultPartition: 3, Target: r.Target, Operations: []core.Operation{core.Send, core.HistoryRead}}}
	now := time.Now()
	o := kafkaSendOutcome()
	scope := kafkaResponseScope("realm", r.Principal, "binding", b, 23, o.RequestID)
	wire, err := kafkaResultMessage(sealer, scope, b, 23, o, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	var got KafkaResult
	got = KafkaResult{}
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	id, _ := config.KafkaRecordID(b.Namespace, b.Route.Topic, b.Route.Partition, 23)
	if got.Version != 1 || got.Protocol != "kafka" || got.RecordID != id || got.RequestID != o.RequestID || got.Namespace != b.Namespace || got.Topic != b.Route.Topic || got.Partition != 2 || got.Offset != 23 || got.ResultTopic != b.Route.ResultTopic || got.ResultPartition != 3 || got.Target != r.Target || !reflect.DeepEqual(got.Result, o.Result) || got.Error != nil {
		t.Fatal("record correlation or acceptance changed", got)
	}
	for _, mutate := range []func(*config.Kafka){
		func(b *config.Kafka) { b.Namespace = "other" },
		func(b *config.Kafka) { b.Route.Topic = "other" },
		func(b *config.Kafka) { b.Route.Partition++ },
		func(b *config.Kafka) { b.Route.ResultTopic = "other" },
		func(b *config.Kafka) { b.Route.ResultPartition++ },
		func(b *config.Kafka) { b.Route.Target.Name = "#other" },
		func(b *config.Kafka) { b.Route.Target.Kind = "agent" },
	} {
		other := b
		mutate(&other)
		expected := kafkaResponseScope(scope.Realm, scope.Principal, scope.Binding, other, 23, o.RequestID)
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, expected, now); err == nil {
			t.Fatal("foreign route accepted")
		}
		if _, err := kafkaResultMessage(sealer, scope, other, 23, o, now); err == nil {
			t.Fatal("foreign route signed")
		}
	}
	for _, expected := range []auth.ResponseScope{
		kafkaResponseScope(scope.Realm, scope.Principal, scope.Binding, b, 24, o.RequestID),
		kafkaResponseScope(scope.Realm, scope.Principal, scope.Binding, b, 23, "other"),
		{Realm: "other", Principal: scope.Principal, Binding: scope.Binding, Route: scope.Route},
		{Realm: scope.Realm, Principal: "other", Binding: scope.Binding, Route: scope.Route},
		{Realm: scope.Realm, Principal: scope.Principal, Binding: "other", Route: scope.Route},
	} {
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, expected, now); err == nil {
			t.Fatal("foreign identity/record accepted")
		}
	}
	o = kafkaOutcome{Version: 1, RequestID: o.RequestID, Operation: core.HistoryRead, Result: &dispatch.DetachedResult{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Cursor: "whole-游標\n"}}}
	wire, err = kafkaResultMessage(sealer, scope, b, 23, o, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	got = KafkaResult{}
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Result, o.Result) {
		t.Fatal("page was shortened")
	}
	o.Result.Page.Cursor = strings.Repeat("x", 24500)
	if _, err := encodeKafkaOutcome(o); err != nil {
		t.Fatal("cache bound should fit", err)
	}
	if wire, err := kafkaResultMessage(sealer, scope, b, 23, o, now); !errors.Is(err, auth.ErrResponseCapacity) || wire != nil {
		t.Fatal("final JWS bound ignored", len(wire), err)
	}
}
