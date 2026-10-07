package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func offsetReply() []byte {
	body := []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 6, 'e', 'v', 'e', 'n', 't', 's', 0, 0, 0, 1, 0, 0, 0, 2, 0, 0}
	body = binary.BigEndian.AppendUint64(body, ^uint64(0))
	body = binary.BigEndian.AppendUint64(body, 123)
	return binary.BigEndian.AppendUint32(body, 4)
}

func TestKafkaListOffsetsExactAndScope(t *testing.T) {
	body, err := ListOffsets("events", 2, 4, -2)
	expected := []byte{255, 255, 255, 255, 1, 0, 0, 0, 1, 0, 6, 'e', 'v', 'e', 'n', 't', 's', 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 4, 255, 255, 255, 255, 255, 255, 255, 254}
	if err != nil || !bytes.Equal(body, expected) {
		t.Fatal("request", body, err)
	}
	valid := offsetReply()
	value, err := ListOffsetsReply(valid, "events", 2)
	if err != nil || value != (Offset{Timestamp: -1, Position: 123, LeaderEpoch: 4}) {
		t.Fatal("reply", value, err)
	}
	for n := 0; n < len(valid); n++ {
		if got, err := ListOffsetsReply(valid[:n], "events", 2); !errors.Is(err, ErrFrame) || got != (Offset{}) {
			t.Fatal("truncation", n)
		}
	}
	for _, mutate := range []func([]byte){func(b []byte) { b[0] = 255 }, func(b []byte) { b[7] = 2 }, func(b []byte) { b[10] = 'x' }, func(b []byte) { b[19] = 2 }, func(b []byte) { b[23] = 3 }, func(b []byte) { b[25] = 1 }, func(b []byte) { b[34] = 255 }} {
		b := bytes.Clone(valid)
		mutate(b)
		if _, err := ListOffsetsReply(b, "events", 2); !errors.Is(err, ErrFrame) {
			t.Fatal("invalid reply", b)
		}
	}
	if _, err := ListOffsetsReply(append(valid, 0), "events", 2); !errors.Is(err, ErrFrame) {
		t.Fatal("trailing")
	}
	for _, v := range []struct {
		topic            string
		partition, epoch int32
		timestamp        int64
	}{{"bad topic", 0, 0, 0}, {"events", -1, 0, 0}, {"events", 0, -2, 0}, {"events", 0, 0, -3}} {
		if body, err := ListOffsets(v.topic, v.partition, v.epoch, v.timestamp); !errors.Is(err, ErrFrame) || body != nil {
			t.Fatal("invalid request", v)
		}
	}
}
