package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func multiBatch(t *testing.T, count int, record func(int) []byte) []byte {
	t.Helper()
	batch := fixtureBatch(t)[:61]
	binary.BigEndian.PutUint32(batch[57:], uint32(count))
	binary.BigEndian.PutUint32(batch[23:], uint32(count-1))
	binary.BigEndian.PutUint64(batch[35:], 2000)
	for i := range count {
		data := record(i)
		batch = binary.AppendVarint(batch, int64(len(data)))
		batch = append(batch, data...)
	}
	binary.BigEndian.PutUint32(batch[8:], uint32(len(batch)-12))
	fixCRC(batch)
	return batch
}

func dataRecord(offset, delta int64, headers []byte) []byte {
	record := []byte{0}
	record = binary.AppendVarint(record, delta)
	record = binary.AppendVarint(record, offset)
	record = append(record, 2, 'k', 2, 'v')
	return append(record, headers...)
}

func TestKafkaMultipleRecordsAndHeaders(t *testing.T) {
	batch := multiBatch(t, 256, func(i int) []byte { return dataRecord(int64(i), int64(i), []byte{0}) })
	got, err := Records(batch)
	if err != nil || len(got) != 1 || len(got[0].Records) != 256 || got[0].Records[255].Offset != 255 || got[0].Records[255].Timestamp != 1255 {
		t.Fatal("record limit", err)
	}
	extra := fixtureBatch(t)
	binary.BigEndian.PutUint64(extra, 256)
	if result, err := Records(append(batch, extra...)); !errors.Is(err, ErrFrame) || result != nil {
		t.Fatal("aggregate count limit")
	}
	// Two headers: empty key/empty value, and key=x/null value.
	headers := []byte{4, 0, 0, 2, 'x', 1}
	withHeaders := multiBatch(t, 1, func(int) []byte { return dataRecord(0, -1, headers) })
	if got, err := Records(withHeaders); err != nil || got[0].Records[0].Timestamp != 999 {
		t.Fatal("headers and negative timestamp delta", err)
	}
	for _, headers := range [][]byte{{1}, {66}, {2, 1}, {2, 2, 255, 0}, {2, 2, 'x', 3}, {2, 0, 0, 0}} {
		batch := multiBatch(t, 1, func(int) []byte { return dataRecord(0, 0, headers) })
		if _, err := Records(batch); !errors.Is(err, ErrFrame) {
			t.Fatal("malformed headers", headers)
		}
	}
	duplicate := multiBatch(t, 2, func(int) []byte { return dataRecord(0, 0, []byte{0}) })
	if _, err := Records(duplicate); !errors.Is(err, ErrFrame) {
		t.Fatal("duplicate offsets")
	}
}

func TestKafkaRecordTimestampOverflowAndVarints(t *testing.T) {
	for _, delta := range []int64{math.MaxInt64, math.MinInt64, -1001} {
		body := multiBatch(t, 1, func(int) []byte { return dataRecord(0, delta, []byte{0}) })
		if got, err := Records(body); !errors.Is(err, ErrFrame) || got != nil {
			t.Fatal("timestamp overflow", delta)
		}
	}
	for _, invalid := range [][]byte{{128}, {128, 0}, {255, 255, 255, 255, 255, 255, 255, 255, 255, 255}, {128, 128, 128, 128, 16}} {
		record := append([]byte{0, 0}, invalid...)
		record = append(record, 2, 'k', 2, 'v', 0)
		body := multiBatch(t, 1, func(int) []byte { return record })
		if _, err := Records(body); !errors.Is(err, ErrFrame) {
			t.Fatal("invalid offset varint", invalid)
		}
	}
	body := multiBatch(t, 1, func(int) []byte { return dataRecord(0, 0, []byte{0}) })
	// Overlong record length is invalid even if its numerical value is correct.
	overlong := append(bytes.Clone(body[:61]), body[61]|128, 0)
	overlong = append(overlong, body[62:]...)
	binary.BigEndian.PutUint32(overlong[8:], uint32(len(overlong)-12))
	fixCRC(overlong)
	if _, err := Records(overlong); !errors.Is(err, ErrFrame) {
		t.Fatal("noncanonical record length")
	}
}
