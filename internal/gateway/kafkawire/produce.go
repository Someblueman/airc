package kafkawire

import (
	"encoding/binary"
	"github.com/Someblueman/airc/internal/gateway/config"
)

// Produce encodes v7 with all-replica acknowledgements and no transaction ID.
func Produce(topic string, partition int32, key, value []byte, timestamp int64) ([]byte, error) {
	if !config.KafkaTopic(topic) || partition < 0 {
		return nil, ErrFrame
	}
	batch, err := RecordBatch(key, value, timestamp)
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint16(nil, 0xffff)
	b = binary.BigEndian.AppendUint16(b, 0xffff)
	b = binary.BigEndian.AppendUint32(b, 1000)
	b = binary.BigEndian.AppendUint32(b, 1)
	b = appendText(b, topic)
	b = binary.BigEndian.AppendUint32(b, 1)
	b = binary.BigEndian.AppendUint32(b, uint32(partition))
	b = binary.BigEndian.AppendUint32(b, uint32(len(batch)))
	return append(b, batch...), nil
}

type ProduceResult struct{ BaseOffset, LogAppendTime, LogStartOffset int64 }

// ProduceReply confirms broker publication only, never native AIRC acceptance.
func ProduceReply(body []byte, topic string, partition int32) (ProduceResult, error) {
	if len(body) > MaxFrame || !config.KafkaTopic(topic) || partition < 0 {
		return ProduceResult{}, ErrFrame
	}
	d := decoder{body: body}
	topics, name, partitions, index, code := d.i32(), d.text(249, false), d.i32(), d.i32(), d.i16()
	r := ProduceResult{BaseOffset: d.i64(), LogAppendTime: d.i64(), LogStartOffset: d.i64()}
	throttle := d.i32()
	if !d.done() || topics != 1 || name != topic || partitions != 1 || index != partition || code != 0 || throttle < 0 || r.BaseOffset < 0 || r.LogAppendTime < -1 || r.LogStartOffset < -1 || r.LogStartOffset > r.BaseOffset {
		return ProduceResult{}, ErrFrame
	}
	return r, nil
}
