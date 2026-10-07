package kafkawire

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"unicode/utf8"
)

const MaxRecords = 256
const MaxBatches = 64
const MaxValue = 32768

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

type Record struct {
	Offset, Timestamp int64
	Key, Value        []byte
}
type Batch struct {
	BaseOffset, LastOffset int64
	LeaderEpoch            int32
	Records                []Record
}

// RecordBatch emits a single uncompressed nontransactional magic-2 record for
// Produce. Broker-assigned offset/epoch are placeholders; producer identity is
// absent. The caller supplies a bounded key and whole signed result value.
func RecordBatch(key, value []byte, timestamp int64) ([]byte, error) {
	if len(key) < 1 || len(key) > 64 || len(value) < 1 || len(value) > MaxValue || timestamp < 0 {
		return nil, ErrFrame
	}
	record := []byte{0, 0, 0} // Attributes, timestamp delta, offset delta.
	record = binary.AppendVarint(record, int64(len(key)))
	record = append(record, key...)
	record = binary.AppendVarint(record, int64(len(value)))
	record = append(record, value...)
	record = append(record, 0) // No headers.
	data := binary.AppendVarint(nil, int64(len(record)))
	data = append(data, record...)
	batch := make([]byte, 61, len(data)+61)
	binary.BigEndian.PutUint32(batch[8:], uint32(49+len(data)))
	binary.BigEndian.PutUint32(batch[12:], 0xffffffff)
	batch[16] = 2
	binary.BigEndian.PutUint64(batch[27:], uint64(timestamp))
	binary.BigEndian.PutUint64(batch[35:], uint64(timestamp))
	binary.BigEndian.PutUint64(batch[43:], math.MaxUint64)
	binary.BigEndian.PutUint16(batch[51:], math.MaxUint16)
	binary.BigEndian.PutUint32(batch[53:], math.MaxUint32)
	binary.BigEndian.PutUint32(batch[57:], 1)
	batch = append(batch, data...)
	binary.BigEndian.PutUint32(batch[17:], crc32.Checksum(batch[21:], castagnoli))
	return batch, nil
}

// Records validates the whole record set before returning any owned records.
// Empty compacted batches and offset gaps are preserved through LastOffset;
// unsupported compression/transaction/control/delete-horizon batches fail closed.
func Records(body []byte) ([]Batch, error) {
	if len(body) > MaxFrame {
		return nil, ErrFrame
	}
	var batches []Batch
	total := 0
	previous := int64(-1)
	for len(body) > 0 {
		if len(batches) >= MaxBatches || len(body) < 61 {
			return nil, ErrFrame
		}
		size := int64(int32(binary.BigEndian.Uint32(body[8:]))) + 12
		if size < 61 || size > int64(len(body)) {
			return nil, ErrFrame
		}
		wire := body[:int(size)]
		body = body[int(size):]
		base := int64(binary.BigEndian.Uint64(wire))
		epoch := int32(binary.BigEndian.Uint32(wire[12:]))
		attributes := binary.BigEndian.Uint16(wire[21:])
		delta := int32(binary.BigEndian.Uint32(wire[23:]))
		timestamp := int64(binary.BigEndian.Uint64(wire[27:]))
		maximum := int64(binary.BigEndian.Uint64(wire[35:]))
		producer := int64(binary.BigEndian.Uint64(wire[43:]))
		producerEpoch := int16(binary.BigEndian.Uint16(wire[51:]))
		sequence := int32(binary.BigEndian.Uint32(wire[53:]))
		count := int32(binary.BigEndian.Uint32(wire[57:]))
		if wire[16] != 2 || crc32.Checksum(wire[21:], castagnoli) != binary.BigEndian.Uint32(wire[17:]) || attributes & ^uint16(8) != 0 || base < 0 || base <= previous || epoch < -1 || delta < 0 || base > math.MaxInt64-int64(delta) || count < 0 || count > MaxRecords || total+int(count) > MaxRecords || producer < -1 || producerEpoch < -1 || sequence < -1 {
			return nil, ErrFrame
		}
		if producer == -1 && (producerEpoch != -1 || sequence != -1) || producer >= 0 && (producerEpoch < 0 || sequence < 0) {
			return nil, ErrFrame
		}
		if maximum < -1 || count > 0 && (timestamp < 0 || maximum < 0 || attributes&8 == 0 && maximum < timestamp) || count == 0 && timestamp != -1 {
			return nil, ErrFrame
		}
		batch := Batch{BaseOffset: base, LastOffset: base + int64(delta), LeaderEpoch: epoch}
		data := decoder{body: wire[61:]}
		lastDelta := int64(-1)
		for range int(count) {
			length := recordVarint(&data, math.MaxInt32)
			if data.bad || length < 0 || length > int64(len(data.body)) {
				return nil, ErrFrame
			}
			record := decoder{body: data.take(int(length))}
			flags := record.take(1)
			timeDelta := recordVarint(&record, math.MaxInt64)
			offsetDelta := recordVarint(&record, math.MaxInt32)
			key := recordBytes(&record, 64, false, false)
			value := recordBytes(&record, MaxValue, false, false)
			headers := recordVarint(&record, 32)
			if record.bad || len(flags) != 1 || flags[0] != 0 || offsetDelta < 0 || offsetDelta <= lastDelta || offsetDelta > int64(delta) || headers < 0 {
				return nil, ErrFrame
			}
			for range int(headers) {
				headerKey := recordBytes(&record, 255, false, true)
				if !utf8.Valid(headerKey) {
					record.bad = true
				}
				_ = recordBytes(&record, MaxValue, true, true)
			}
			actual, ok := addTimestamp(timestamp, timeDelta)
			if attributes&8 != 0 {
				actual = maximum
				ok = actual >= 0
			}
			if !record.done() || !ok || actual > maximum {
				return nil, ErrFrame
			}
			batch.Records = append(batch.Records, Record{Offset: base + offsetDelta, Timestamp: actual, Key: bytes.Clone(key), Value: bytes.Clone(value)})
			lastDelta = offsetDelta
		}
		if !data.done() {
			return nil, ErrFrame
		}
		total += int(count)
		previous = batch.LastOffset
		batches = append(batches, batch)
	}
	return batches, nil
}

func recordVarint(d *decoder, max int64) int64 {
	if d.bad {
		return 0
	}
	value, n := binary.Varint(d.body)
	if n <= 0 || value > max || max == math.MaxInt32 && (value < math.MinInt32 || n > 5) {
		d.bad = true
		return 0
	}
	var canonical [10]byte
	if binary.PutVarint(canonical[:], value) != n {
		d.bad = true
		return 0
	}
	d.body = d.body[n:]
	return value
}
func recordBytes(d *decoder, max int, nullable, empty bool) []byte {
	size := recordVarint(d, math.MaxInt32)
	if size == -1 && nullable {
		return nil
	}
	if size < 0 || size == 0 && !empty || size > int64(max) {
		d.bad = true
		return nil
	}
	return d.take(int(size))
}
func addTimestamp(base, delta int64) (int64, bool) {
	if delta > 0 && base > math.MaxInt64-delta || delta < 0 && base < math.MinInt64-delta {
		return 0, false
	}
	value := base + delta
	return value, value >= 0
}
