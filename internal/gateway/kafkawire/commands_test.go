package kafkawire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func fetchFixture(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString("000000000000000000000000000100017400000001000000020000000000000000001400000000000000140000000000000000ffffffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := RecordBatch([]byte("id"), []byte("value"), 100)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint64(batch, 10)
	b = binary.BigEndian.AppendUint32(b, uint32(len(batch)))
	return append(b, batch...)
}

func TestFetchCommands(t *testing.T) {
	b, err := Fetch("t", 2, 3, 10)
	want, _ := hex.DecodeString("ffffffff000003e800000001000ff0000100000000ffffffff00000001000174000000010000000200000003000000000000000affffffffffffffff000ff000000000000000")
	if err != nil || !bytes.Equal(b, want) {
		t.Fatalf("request %x %v", b, err)
	}
	reply := fetchFixture(t)
	r, err := FetchReply(reply, "t", 2)
	if err != nil || r.HighWatermark != 20 || r.LastStableOffset != 20 || len(r.Batches) != 1 || r.Batches[0].Records[0].Offset != 10 {
		t.Fatalf("reply %+v %v", r, err)
	}
	for n := 0; n < len(reply); n++ {
		if r, e := FetchReply(reply[:n], "t", 2); e == nil || r.Batches != nil {
			t.Fatalf("truncation %d", n)
		}
	}
	for _, off := range []int{0, 4, 6, 10, 16, 17, 21, 25, 27, 35, 43, 51, 55, 59} {
		bad := bytes.Clone(reply)
		bad[off] = 0x80
		if r, e := FetchReply(bad, "t", 2); e == nil || r.Batches != nil {
			t.Fatalf("field %d", off)
		}
	}
	bad := bytes.Clone(reply)
	bad[len(bad)-1] ^= 1
	if r, e := FetchReply(bad, "t", 2); e == nil || r.Batches != nil {
		t.Fatal("batch CRC accepted")
	}
	if _, e := FetchReply(append(bytes.Clone(reply), 0), "t", 2); e == nil {
		t.Fatal("trailing byte")
	}
	if _, e := FetchReply(reply, "other", 2); e == nil {
		t.Fatal("topic scope")
	}
	if _, e := FetchReply(reply, "t", 3); e == nil {
		t.Fatal("partition scope")
	}
	// Null and empty records are valid successful replies.
	for _, size := range []uint32{0, 0xffffffff} {
		empty := bytes.Clone(reply[:59])
		empty = binary.BigEndian.AppendUint32(empty, size)
		if r, e := FetchReply(empty, "t", 2); e != nil || len(r.Batches) != 0 {
			t.Fatalf("empty %x %+v %v", size, r, e)
		}
	}
	for _, args := range []struct {
		topic string
		p, e  int32
		o     int64
	}{{"", 0, 0, 0}, {"t", -1, 0, 0}, {"t", 0, -2, 0}, {"t", 0, 0, -1}} {
		if _, e := Fetch(args.topic, args.p, args.e, args.o); e == nil {
			t.Fatal("invalid fetch")
		}
	}
}

func TestProduceCommands(t *testing.T) {
	b, err := Produce("t", 2, []byte("id"), []byte("value"), 100)
	prefix, _ := hex.DecodeString("ffffffff000003e8000000010001740000000100000002")
	if err != nil || !bytes.Equal(b[:len(prefix)], prefix) {
		t.Fatalf("request %x %v", b, err)
	}
	batch := b[len(prefix)+4:]
	if int(binary.BigEndian.Uint32(b[len(prefix):])) != len(batch) {
		t.Fatal("batch size")
	}
	records, err := Records(batch)
	if err != nil || len(records) != 1 || string(records[0].Records[0].Value) != "value" {
		t.Fatalf("batch %v %v", records, err)
	}
	reply, _ := hex.DecodeString("0000000100017400000001000000020000000000000000000affffffffffffffff000000000000000000000000")
	r, err := ProduceReply(reply, "t", 2)
	if err != nil || r.BaseOffset != 10 || r.LogAppendTime != -1 || r.LogStartOffset != 0 {
		t.Fatalf("reply %+v %v", r, err)
	}
	for n := 0; n < len(reply); n++ {
		if r, e := ProduceReply(reply[:n], "t", 2); e == nil || r != (ProduceResult{}) {
			t.Fatalf("truncation %d", n)
		}
	}
	for _, off := range []int{0, 7, 11, 15, 17, 33, 41} {
		bad := bytes.Clone(reply)
		bad[off] = 0x80
		if r, e := ProduceReply(bad, "t", 2); e == nil || r != (ProduceResult{}) {
			t.Fatalf("field %d", off)
		}
	}
	for _, body := range [][]byte{append(bytes.Clone(reply), 0), make([]byte, MaxFrame+1)} {
		if _, e := ProduceReply(body, "t", 2); e == nil {
			t.Fatal("reply bounds")
		}
	}
	if _, e := ProduceReply(reply, "other", 2); e == nil {
		t.Fatal("topic scope")
	}
	if _, e := ProduceReply(reply, "t", 3); e == nil {
		t.Fatal("partition scope")
	}
	if _, e := Produce("", 0, []byte("id"), []byte("v"), 0); e == nil {
		t.Fatal("invalid topic")
	}
	if _, e := Produce("t", -1, []byte("id"), []byte("v"), 0); e == nil {
		t.Fatal("invalid partition")
	}
	if _, e := Produce("t", 0, nil, []byte("v"), 0); e == nil {
		t.Fatal("invalid batch")
	}
}
