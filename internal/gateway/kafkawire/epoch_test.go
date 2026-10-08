package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestEpochEnd(t *testing.T) {
	body, err := EpochEnd("events", 2, 9, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{255, 255, 255, 255, 0, 0, 0, 1, 0, 6, 'e', 'v', 'e', 'n', 't', 's', 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 9, 0, 0, 0, 8}
	if !bytes.Equal(body, want) {
		t.Fatalf("request %x", body)
	}
	for _, tc := range []struct {
		topic                     string
		partition, current, saved int32
	}{
		{"", 2, 9, 8}, {"events", -1, 9, 8}, {"events", 2, 9, -1}, {"events", 2, 7, 8},
	} {
		if _, err := EpochEnd(tc.topic, tc.partition, tc.current, tc.saved); !errors.Is(err, ErrFrame) {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestEpochEndReply(t *testing.T) {
	body := binary.BigEndian.AppendUint32(nil, 0)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = appendText(body, "events")
	body = binary.BigEndian.AppendUint32(body, 1)
	body = binary.BigEndian.AppendUint16(body, 0)
	body = binary.BigEndian.AppendUint32(body, 2)
	body = binary.BigEndian.AppendUint32(body, 8)
	body = binary.BigEndian.AppendUint64(body, 23)
	got, err := EpochEndReply(body, "events", 2, 8)
	if err != nil || got != (EpochBoundary{8, 23}) {
		t.Fatalf("%+v %v", got, err)
	}
	// Earlier epochs are legal broker answers; the owner compares the end offset.
	earlier := bytes.Clone(body)
	binary.BigEndian.PutUint32(earlier[len(earlier)-12:], 7)
	if got, err := EpochEndReply(earlier, "events", 2, 8); err != nil || got.LeaderEpoch != 7 {
		t.Fatalf("%+v %v", got, err)
	}
	for i := 0; i < len(body); i++ {
		if _, err := EpochEndReply(body[:i], "events", 2, 8); !errors.Is(err, ErrFrame) {
			t.Fatalf("truncation %d", i)
		}
	}
	cases := [][]byte{append(bytes.Clone(body), 0)}
	for _, edit := range []struct {
		offset int
		value  uint32
	}{{0, 0xffffffff}, {4, 2}, {16, 2}, {22, 3}, {26, 9}, {26, 0xffffffff}, {30, 0xffffffff}} {
		changed := bytes.Clone(body)
		binary.BigEndian.PutUint32(changed[edit.offset:], edit.value)
		cases = append(cases, changed)
	}
	failed := bytes.Clone(body)
	binary.BigEndian.PutUint16(failed[20:], 1)
	cases = append(cases, failed)
	for i, b := range cases {
		if _, err := EpochEndReply(b, "events", 2, 8); !errors.Is(err, ErrFrame) {
			t.Fatalf("accepted malformed %d", i)
		}
	}
	for _, tc := range []struct {
		topic            string
		partition, epoch int32
	}{{"other", 2, 8}, {"events", 3, 8}, {"events", 2, -1}} {
		if _, err := EpochEndReply(body, tc.topic, tc.partition, tc.epoch); !errors.Is(err, ErrFrame) {
			t.Fatalf("accepted scope %+v", tc)
		}
	}
}
