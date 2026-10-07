package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestKafkaRecordMalformedBatchRefusesWholeSet(t *testing.T) {
	valid := fixtureBatch(t)
	for n := 1; n < len(valid); n++ {
		if got, err := Records(valid[:n]); !errors.Is(err, ErrFrame) || got != nil {
			t.Fatal("truncation", n)
		}
	}
	mutations := map[string]func([]byte){
		"crc": func(b []byte) { b[17] ^= 1 }, "magic": func(b []byte) { b[16] = 1 },
		"length":          func(b []byte) { binary.BigEndian.PutUint32(b[8:], math.MaxUint32) },
		"base":            func(b []byte) { binary.BigEndian.PutUint64(b, math.MaxUint64) },
		"epoch":           func(b []byte) { binary.BigEndian.PutUint32(b[12:], math.MaxUint32-1) },
		"delta":           func(b []byte) { binary.BigEndian.PutUint32(b[23:], math.MaxUint32) },
		"offset_overflow": func(b []byte) { binary.BigEndian.PutUint64(b, math.MaxInt64); binary.BigEndian.PutUint32(b[23:], 1) },
		"time":            func(b []byte) { binary.BigEndian.PutUint64(b[27:], math.MaxUint64) },
		"max_time":        func(b []byte) { binary.BigEndian.PutUint64(b[35:], 999) },
		"producer":        func(b []byte) { binary.BigEndian.PutUint64(b[43:], 1) },
		"count":           func(b []byte) { binary.BigEndian.PutUint32(b[57:], 257) },
		"empty_nonempty":  func(b []byte) { binary.BigEndian.PutUint32(b[57:], 0) },
		"record_size":     func(b []byte) { b[61] = 0 }, "record_flags": func(b []byte) { b[62] = 1 },
		"record_delta": func(b []byte) { b[64] = 2 }, "null_key": func(b []byte) { b[65] = 1 },
		"value_size": func(b []byte) { b[68] = 0 }, "headers": func(b []byte) { b[len(b)-1] = 2 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b := bytes.Clone(valid)
			mutate(b)
			if name != "crc" {
				fixCRC(b)
			}
			if got, err := Records(b); !errors.Is(err, ErrFrame) || got != nil {
				t.Fatal("invalid batch", got, err)
			}
			prefix := fixtureBatch(t)
			if name != "base" && name != "offset_overflow" {
				binary.BigEndian.PutUint64(b, 5)
			}
			if got, err := Records(append(prefix, b...)); !errors.Is(err, ErrFrame) || got != nil {
				t.Fatal("returned partial set")
			}
		})
	}
	for _, flag := range []uint16{1, 2, 3, 4, 16, 32, 64, 128} {
		b := bytes.Clone(valid)
		binary.BigEndian.PutUint16(b[21:], flag)
		fixCRC(b)
		if got, err := Records(b); !errors.Is(err, ErrFrame) || got != nil {
			t.Fatal("unsupported attributes", flag)
		}
	}
	if _, err := Records(append(bytes.Clone(valid), valid...)); !errors.Is(err, ErrFrame) {
		t.Fatal("overlapping batches")
	}
	if _, err := Records(bytes.Repeat([]byte{0}, MaxFrame+1)); !errors.Is(err, ErrFrame) {
		t.Fatal("frame bound")
	}
}

func TestKafkaRecordBatchCountAndValueLimits(t *testing.T) {
	var wire []byte
	for i := range MaxBatches {
		batch := fixtureBatch(t)
		binary.BigEndian.PutUint64(batch, uint64(i))
		wire = append(wire, batch...)
	}
	if got, err := Records(wire); err != nil || len(got) != MaxBatches {
		t.Fatal("batch limit", err)
	}
	extra := fixtureBatch(t)
	binary.BigEndian.PutUint64(extra, MaxBatches)
	if _, err := Records(append(wire, extra...)); !errors.Is(err, ErrFrame) {
		t.Fatal("too many batches")
	}
	for _, v := range []struct {
		key, value []byte
		timestamp  int64
	}{{nil, []byte("v"), 0}, {[]byte("k"), nil, 0}, {bytes.Repeat([]byte("k"), 65), []byte("v"), 0}, {[]byte("k"), bytes.Repeat([]byte("v"), MaxValue+1), 0}, {[]byte("k"), []byte("v"), -1}} {
		if got, err := RecordBatch(v.key, v.value, v.timestamp); !errors.Is(err, ErrFrame) || got != nil {
			t.Fatal("invalid encoder input")
		}
	}
	max, err := RecordBatch(bytes.Repeat([]byte("k"), 64), bytes.Repeat([]byte("v"), MaxValue), math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Records(max); err != nil || len(got[0].Records[0].Value) != MaxValue {
		t.Fatal("exact bounds", err)
	}
}
