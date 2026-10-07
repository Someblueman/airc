package kafkawire

import (
	"encoding/binary"
	"github.com/Someblueman/airc/internal/gateway/config"
)

// Fetch encodes v11 for one partition, without incremental sessions. The frame
// ceiling still applies when a broker exceeds the requested byte budget.
func Fetch(topic string, partition, epoch int32, offset int64) ([]byte, error) {
	if !config.KafkaTopic(topic) || partition < 0 || epoch < -1 || offset < 0 {
		return nil, ErrFrame
	}
	b := binary.BigEndian.AppendUint32(nil, 0xffffffff)
	for _, n := range []uint32{1000, 1, MaxFrame - 4096} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	b = append(b, 1)
	for _, n := range []uint32{0, 0xffffffff, 1} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	b = appendText(b, topic)
	for _, n := range []uint32{1, uint32(partition), uint32(epoch)} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	b = binary.BigEndian.AppendUint64(b, uint64(offset))
	b = binary.BigEndian.AppendUint64(b, 0xffffffffffffffff)
	b = binary.BigEndian.AppendUint32(b, MaxFrame-4096)
	b = binary.BigEndian.AppendUint32(b, 0)
	return appendText(b, ""), nil
}

type FetchResult struct {
	HighWatermark, LastStableOffset, LogStartOffset int64
	Batches                                         []Batch
}

// FetchReply validates the entire v11 reply before exposing records. A batch
// can begin before the requested position after compaction; the owner filters
// retained offsets and never silently resets its journal position.
func FetchReply(body []byte, topic string, partition int32) (FetchResult, error) {
	if len(body) > MaxFrame || !config.KafkaTopic(topic) || partition < 0 {
		return FetchResult{}, ErrFrame
	}
	d := decoder{body: body}
	throttle, code, session := d.i32(), d.i16(), d.i32()
	topics, name, partitions, index := d.i32(), d.text(249, false), d.i32(), d.i32()
	partitionCode := d.i16()
	r := FetchResult{HighWatermark: d.i64(), LastStableOffset: d.i64(), LogStartOffset: d.i64()}
	aborted := d.i32()
	if aborted != -1 && aborted != 0 {
		return FetchResult{}, ErrFrame
	}
	replica, size := d.i32(), d.i32()
	if size < -1 || size > MaxFrame {
		return FetchResult{}, ErrFrame
	}
	var records []byte
	if size >= 0 {
		records = d.take(int(size))
	}
	if !d.done() || throttle < 0 || code != 0 || session != 0 || topics != 1 || name != topic || partitions != 1 || index != partition || partitionCode != 0 || replica < -1 || r.LogStartOffset < 0 || r.LastStableOffset < r.LogStartOffset || r.HighWatermark < r.LastStableOffset {
		return FetchResult{}, ErrFrame
	}
	batches, err := Records(records)
	if err != nil {
		return FetchResult{}, err
	}
	for _, batch := range batches {
		if batch.LastOffset >= r.LastStableOffset {
			return FetchResult{}, ErrFrame
		}
	}
	r.Batches = batches
	return r, nil
}
