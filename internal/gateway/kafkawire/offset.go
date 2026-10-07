package kafkawire

import (
	"encoding/binary"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// ListOffsets encodes v5 for one fixed partition with read-committed isolation.
// Timestamp -2 selects earliest, -1 latest; nonnegative values query a timestamp.
// The owner must never use this command to silently reset a retained position.
func ListOffsets(topic string, partition, epoch int32, timestamp int64) ([]byte, error) {
	if !config.KafkaTopic(topic) || partition < 0 || epoch < -1 || timestamp < -2 {
		return nil, ErrFrame
	}
	body := binary.BigEndian.AppendUint32(nil, 0xffffffff)
	body = append(body, 1)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = appendText(body, topic)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = binary.BigEndian.AppendUint32(body, uint32(partition))
	body = binary.BigEndian.AppendUint32(body, uint32(epoch))
	body = binary.BigEndian.AppendUint64(body, uint64(timestamp))
	return body, nil
}

type Offset struct {
	Timestamp, Position int64
	LeaderEpoch         int32
}

// ListOffsetsReply accepts exactly the requested partition and a successful v5
// result. Unavailable positions, broker errors and additional results are refused.
func ListOffsetsReply(body []byte, topic string, partition int32) (Offset, error) {
	if len(body) > MaxFrame || !config.KafkaTopic(topic) || partition < 0 {
		return Offset{}, ErrFrame
	}
	d := decoder{body: body}
	throttle := d.i32()
	topics := d.i32()
	name := d.text(249, false)
	partitions := d.i32()
	index := d.i32()
	code := d.i16()
	value := Offset{Timestamp: d.i64(), Position: d.i64(), LeaderEpoch: d.i32()}
	if !d.done() || throttle < 0 || topics != 1 || name != topic || partitions != 1 || index != partition || code != 0 || value.Timestamp < -1 || value.Position < 0 || value.LeaderEpoch < -1 {
		return Offset{}, ErrFrame
	}
	return value, nil
}
