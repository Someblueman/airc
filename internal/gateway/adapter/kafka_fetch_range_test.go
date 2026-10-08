package adapter

import (
	"math"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func kafkaRangeRecord(offset int64) kafkawire.Record {
	return kafkawire.Record{Offset: offset, Timestamp: 1000, Key: []byte("original-id"), Value: []byte("whole-signed-request")}
}

func TestKafkaFetchWholeCompactedRange(t *testing.T) {
	position := store.KafkaPosition{NextOffset: 23, LeaderEpoch: 7}
	fetched := kafkawire.FetchResult{LogStartOffset: 10, LastStableOffset: 40, HighWatermark: 50, Batches: []kafkawire.Batch{
		{BaseOffset: 20, LastOffset: 24, LeaderEpoch: 6, Records: []kafkawire.Record{kafkaRangeRecord(22), kafkaRangeRecord(24)}},
		{BaseOffset: 25, LastOffset: 28, LeaderEpoch: 7},
		{BaseOffset: 30, LastOffset: 35, LeaderEpoch: 8, Records: []kafkawire.Record{kafkaRangeRecord(31), kafkaRangeRecord(34)}},
	}}
	plan, err := kafkaFetchRange(position, 8, fetched)
	if err != nil {
		t.Fatal(err)
	}
	if plan.end != 36 || len(plan.records) != 3 {
		t.Fatal("wrong whole coverage", plan)
	}
	for i, offset := range []int64{24, 31, 34} {
		if !reflect.DeepEqual(plan.records[i], kafkaRangeRecord(offset)) {
			t.Fatal("record altered or reordered", plan.records[i])
		}
	}
	fetched.Batches[0].Records[1].Key[0] = 'X'
	fetched.Batches[0].Records[1].Value[0] = 'X'
	if !reflect.DeepEqual(plan.records[0], kafkaRangeRecord(24)) {
		t.Fatal("plan aliases caller data")
	}
	// Computing a plan has no authority to alter its caller's saved position.
	if position != (store.KafkaPosition{NextOffset: 23, LeaderEpoch: 7}) {
		t.Fatal("position changed")
	}
}

func TestKafkaFetchCoverageNeedsReturnedBatches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		position int64
		stable   int64
		batches  []kafkawire.Batch
		end      int64
	}{
		{name: "watermarks alone", position: 23, stable: 40, end: 23},
		{name: "unstable suffix", position: 23, stable: 20, end: 23},
		{name: "empty compacted batch", position: 23, stable: 40, batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 29, LeaderEpoch: 7}}, end: 30},
		{name: "compacted remainder of older batch", position: 23, stable: 40, batches: []kafkawire.Batch{{BaseOffset: 20, LastOffset: 29, LeaderEpoch: 6, Records: []kafkawire.Record{kafkaRangeRecord(22)}}}, end: 30},
		{name: "already consumed batch", position: 23, stable: 40, batches: []kafkawire.Batch{{BaseOffset: 20, LastOffset: 22, LeaderEpoch: 6, Records: []kafkawire.Record{kafkaRangeRecord(22)}}}, end: 23},
		{name: "maximum position", position: math.MaxInt64, stable: math.MaxInt64, end: math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			high := int64(50)
			if tc.position > high {
				high = tc.position
			}
			plan, err := kafkaFetchRange(store.KafkaPosition{NextOffset: tc.position, LeaderEpoch: 7}, 8, kafkawire.FetchResult{LogStartOffset: 10, LastStableOffset: tc.stable, HighWatermark: high, Batches: tc.batches})
			if err != nil || plan.end != tc.end || len(plan.records) != 0 {
				t.Fatal("unproven skip or wrong compacted proof", plan, err)
			}
		})
	}
}
