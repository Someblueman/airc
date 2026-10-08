package adapter

import (
	"bytes"

	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// kafkaRange is an owned plan for a whole validated Fetch response. Records
// must complete in order before the owner may advance through the trailing
// compacted range to end. This plan itself grants no admission or advancement.
type kafkaRange struct {
	records []kafkawire.Record
	end     int64
}

// kafkaFetchRange checks a codec-validated Fetch against the exact position
// used by its request and the metadata-verified current request leader epoch.
// Only returned batches prove coverage: watermarks and an empty Fetch alone
// cannot authorize skipping a position. Historical batch epochs may precede the
// current metadata epoch; an epoch beyond that verified leader is refused.
func kafkaFetchRange(position store.KafkaPosition, leaderEpoch int32, fetched kafkawire.FetchResult) (kafkaRange, error) {
	if position.NextOffset < 0 || position.LeaderEpoch < -1 || leaderEpoch < 0 || leaderEpoch < position.LeaderEpoch || fetched.LogStartOffset < 0 || fetched.LogStartOffset > position.NextOffset || fetched.LastStableOffset < fetched.LogStartOffset || fetched.HighWatermark < fetched.LastStableOffset || position.NextOffset > fetched.HighWatermark || len(fetched.Batches) > kafkawire.MaxBatches {
		return kafkaRange{}, kafkawire.ErrFrame
	}
	plan := kafkaRange{end: position.NextOffset}
	previous := int64(-1)
	count := 0
	for _, batch := range fetched.Batches {
		if batch.BaseOffset < 0 || batch.BaseOffset <= previous || batch.LastOffset < batch.BaseOffset || batch.LastOffset >= fetched.LastStableOffset || batch.LeaderEpoch < -1 || batch.LeaderEpoch > leaderEpoch || len(batch.Records) > kafkawire.MaxRecords-count {
			return kafkaRange{}, kafkawire.ErrFrame
		}
		lastRecord := batch.BaseOffset - 1
		for _, record := range batch.Records {
			if record.Offset <= lastRecord || record.Offset < batch.BaseOffset || record.Offset > batch.LastOffset || record.Timestamp < 0 || len(record.Key) < 1 || len(record.Key) > 64 || len(record.Value) < 1 || len(record.Value) > kafkawire.MaxValue {
				return kafkaRange{}, kafkawire.ErrFrame
			}
			lastRecord = record.Offset
			if record.Offset < position.NextOffset {
				continue
			}
			record.Key = bytes.Clone(record.Key)
			record.Value = bytes.Clone(record.Value)
			plan.records = append(plan.records, record)
		}
		count += len(batch.Records)
		previous = batch.LastOffset
		// LastOffset < LastStableOffset guarantees addition cannot overflow.
		plan.end = max(plan.end, batch.LastOffset+1)
	}
	return plan, nil
}
