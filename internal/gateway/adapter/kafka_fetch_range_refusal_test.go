package adapter

import (
	"errors"
	"math"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaFetchRangeWholeRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*store.KafkaPosition, *int32, *kafkawire.FetchResult)
	}{
		{"negative position", func(p *store.KafkaPosition, _ *int32, _ *kafkawire.FetchResult) { p.NextOffset = -1 }},
		{"invalid saved epoch", func(p *store.KafkaPosition, _ *int32, _ *kafkawire.FetchResult) { p.LeaderEpoch = -2 }},
		{"unverified leader", func(_ *store.KafkaPosition, e *int32, _ *kafkawire.FetchResult) { *e = -1 }},
		{"leader epoch regression", func(_ *store.KafkaPosition, e *int32, _ *kafkawire.FetchResult) { *e = 6 }},
		{"negative log start", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.LogStartOffset = -1 }},
		{"retained position lost", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.LogStartOffset = 24 }},
		{"position beyond high watermark", func(p *store.KafkaPosition, _ *int32, _ *kafkawire.FetchResult) { p.NextOffset = 51 }},
		{"log bounds reversed", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.LastStableOffset = 9 }},
		{"unstable bound reversed", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.HighWatermark = 39 }},
		{"negative batch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].BaseOffset = -1 }},
		{"overlapping batch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].BaseOffset = 24 }},
		{"reversed batch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].LastOffset = 24 }},
		{"uncommitted batch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].LastOffset = 40 }},
		{"overflowing batch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches[1].LastOffset = math.MaxInt64
		}},
		{"future batch epoch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].LeaderEpoch = 9 }},
		{"invalid batch epoch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].LeaderEpoch = -2 }},
		{"record before batch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].Records[0].Offset = 24 }},
		{"record after batch", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].Records[0].Offset = 31 }},
		{"duplicate record", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches[1].Records = append(f.Batches[1].Records, kafkaRangeRecord(26))
		}},
		{"reversed records", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches[1].Records = append(f.Batches[1].Records, kafkaRangeRecord(25))
		}},
		{"negative timestamp", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches[1].Records[0].Timestamp = -1
		}},
		{"empty key", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].Records[0].Key = nil }},
		{"oversized key", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches[1].Records[0].Key = make([]byte, 65)
		}},
		{"empty value", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) { f.Batches[1].Records[0].Value = nil }},
		{"oversized value", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches[1].Records[0].Value = make([]byte, kafkawire.MaxValue+1)
		}},
		{"batch count", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches = make([]kafkawire.Batch, kafkawire.MaxBatches+1)
		}},
		{"record count", func(_ *store.KafkaPosition, _ *int32, f *kafkawire.FetchResult) {
			f.Batches[1].Records = make([]kafkawire.Record, kafkawire.MaxRecords)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			position := store.KafkaPosition{NextOffset: 23, LeaderEpoch: 7}
			epoch := int32(8)
			// Even a bad later record/batch must refuse the whole plan before the
			// earlier valid record can reach signed admission or durable advancement.
			fetched := kafkawire.FetchResult{LogStartOffset: 10, LastStableOffset: 40, HighWatermark: 50, Batches: []kafkawire.Batch{
				{BaseOffset: 23, LastOffset: 24, LeaderEpoch: 7, Records: []kafkawire.Record{kafkaRangeRecord(24)}},
				{BaseOffset: 25, LastOffset: 30, LeaderEpoch: 8, Records: []kafkawire.Record{kafkaRangeRecord(26)}},
			}}
			tc.mutate(&position, &epoch, &fetched)
			plan, err := kafkaFetchRange(position, epoch, fetched)
			if !errors.Is(err, kafkawire.ErrFrame) || plan.end != 0 || plan.records != nil {
				t.Fatal("partial plan escaped a whole refusal", plan, err)
			}
		})
	}
}
