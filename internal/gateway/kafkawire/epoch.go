package kafkawire

import (
	"encoding/binary"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// EpochEnd encodes OffsetForLeaderEpoch v3 for a consumer and one fixed
// partition. The current epoch fences stale metadata; savedEpoch selects the
// history whose end the owner must compare with its retained next offset.
func EpochEnd(topic string, partition, currentEpoch, savedEpoch int32) ([]byte, error) {
	if !config.KafkaTopic(topic) || partition < 0 || savedEpoch < 0 || currentEpoch < savedEpoch {
		return nil, ErrFrame
	}
	body := binary.BigEndian.AppendUint32(nil, 0xffffffff)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = appendText(body, topic)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = binary.BigEndian.AppendUint32(body, uint32(partition))
	body = binary.BigEndian.AppendUint32(body, uint32(currentEpoch))
	body = binary.BigEndian.AppendUint32(body, uint32(savedEpoch))
	return body, nil
}

type EpochBoundary struct {
	LeaderEpoch int32
	EndOffset   int64
}

// EpochEndReply validates the complete v3 reply, including the selected epoch.
// A broker may return an earlier epoch if the requested epoch is absent. The
// owner must refuse divergence when EndOffset is below its retained position.
func EpochEndReply(body []byte, topic string, partition, savedEpoch int32) (EpochBoundary, error) {
	if len(body) > MaxFrame || !config.KafkaTopic(topic) || partition < 0 || savedEpoch < 0 {
		return EpochBoundary{}, ErrFrame
	}
	d := decoder{body: body}
	throttle, topics := d.i32(), d.i32()
	name := d.text(249, false)
	partitions := d.i32()
	code, index := d.i16(), d.i32()
	value := EpochBoundary{LeaderEpoch: d.i32(), EndOffset: d.i64()}
	if !d.done() || throttle < 0 || topics != 1 || name != topic || partitions != 1 || code != 0 || index != partition || value.LeaderEpoch < 0 || value.LeaderEpoch > savedEpoch || value.EndOffset < 0 {
		return EpochBoundary{}, ErrFrame
	}
	return value, nil
}
