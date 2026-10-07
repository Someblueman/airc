package kafkawire

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func fixCRC(body []byte) {
	binary.BigEndian.PutUint32(body[17:], crc32.Checksum(body[21:], castagnoli))
}
func fixtureBatch(t *testing.T) []byte {
	t.Helper()
	body, err := RecordBatch([]byte("id"), []byte("body\x00世界"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestKafkaRecordBatchWireAndOwnership(t *testing.T) {
	body := fixtureBatch(t)
	if len(body) != 81 || body[16] != 2 || binary.BigEndian.Uint32(body[8:]) != 69 || binary.BigEndian.Uint32(body[57:]) != 1 {
		t.Fatal("batch header", body)
	}
	// Independently specified record: size=19, attributes/deltas=0, key=id,
	// value=body + NUL + UTF-8, zero headers. Signed integers use zigzag.
	expected := []byte{38, 0, 0, 0, 4, 'i', 'd', 22, 'b', 'o', 'd', 'y', 0, 0xe4, 0xb8, 0x96, 0xe7, 0x95, 0x8c, 0}
	if !bytes.Equal(body[61:], expected) {
		t.Fatal("record bytes", body[61:])
	}
	batches, err := Records(body)
	if err != nil || len(batches) != 1 || len(batches[0].Records) != 1 {
		t.Fatal(err)
	}
	record := batches[0].Records[0]
	if record.Offset != 0 || record.Timestamp != 1000 || string(record.Key) != "id" || string(record.Value) != "body\x00世界" {
		t.Fatal(record)
	}
	clear(body)
	if string(record.Key) != "id" || string(record.Value) != "body\x00世界" {
		t.Fatal("decoded record aliases wire")
	}
	key, value := []byte("key"), []byte("value")
	encoded, err := RecordBatch(key, value, 0)
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	clear(value)
	decoded, err := Records(encoded)
	if err != nil || string(decoded[0].Records[0].Key) != "key" || string(decoded[0].Records[0].Value) != "value" {
		t.Fatal("encoder aliases input", err)
	}
}

func TestKafkaRecordCompactionGapsAndAppendTime(t *testing.T) {
	first := fixtureBatch(t)
	binary.BigEndian.PutUint64(first, 10)
	binary.BigEndian.PutUint32(first[23:], 5)
	first[64] = 4 // Offset delta 2 within compacted batch range [10,15].
	fixCRC(first)
	second := fixtureBatch(t)
	binary.BigEndian.PutUint64(second, 20)
	got, err := Records(append(first, second...))
	if err != nil || len(got) != 2 || got[0].LastOffset != 15 || got[0].Records[0].Offset != 12 || got[1].BaseOffset != 20 {
		t.Fatal("compacted gaps", got, err)
	}
	empty := bytes.Clone(second[:61])
	binary.BigEndian.PutUint32(empty[8:], 49)
	binary.BigEndian.PutUint32(empty[57:], 0)
	binary.BigEndian.PutUint64(empty[27:], ^uint64(0))
	fixCRC(empty)
	if got, err := Records(empty); err != nil || len(got) != 1 || len(got[0].Records) != 0 || got[0].LastOffset != 20 {
		t.Fatal("empty compacted batch", got, err)
	}
	appendTime := fixtureBatch(t)
	binary.BigEndian.PutUint16(appendTime[21:], 8)
	binary.BigEndian.PutUint64(appendTime[35:], 500)
	fixCRC(appendTime)
	if got, err := Records(appendTime); err != nil || got[0].Records[0].Timestamp != 500 {
		t.Fatal("broker append timestamp", err)
	}
	if got, err := Records(nil); err != nil || len(got) != 0 {
		t.Fatal("empty set", err)
	}
}
