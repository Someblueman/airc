package config

import (
	"math"
	"strings"
	"testing"
)

func TestKafkaRecordIdentity(t *testing.T) {
	value, err := KafkaRecordID("broker", "events", 0, 0)
	if err != nil || value != "8547fc741f73560b1ed93f5a8a142194a1f368bed04d446d51d3f077f14dc933" {
		t.Fatal("fixed vector", value, err)
	}
	for _, v := range []struct {
		namespace, topic string
		partition        int32
		offset           int64
	}{
		{"other", "events", 0, 0}, {"broker", "other", 0, 0}, {"broker", "events", 1, 0}, {"broker", "events", 0, 1},
		{"broker", "events", math.MaxInt32, math.MaxInt64},
	} {
		changed, err := KafkaRecordID(v.namespace, v.topic, v.partition, v.offset)
		if err != nil || changed == value {
			t.Fatal("record identity not bound", v, err)
		}
	}
	for _, v := range []struct {
		namespace, topic string
		partition        int32
		offset           int64
	}{
		{"", "events", 0, 0}, {"bad\x00", "events", 0, 0}, {"broker", "events", -1, 0}, {"broker", "events", 0, -1}, {"broker", "bad\x00", 0, 0},
	} {
		got, err := KafkaRecordID(v.namespace, v.topic, v.partition, v.offset)
		if err == nil || got != "" {
			t.Fatal("invalid identity accepted", v)
		}
	}
}

func TestKafkaLiteralTopicBounds(t *testing.T) {
	for _, topic := range []string{"events", "a.A_0-", strings.Repeat("x", 249)} {
		if !KafkaTopic(topic) {
			t.Fatal("valid topic refused", topic)
		}
	}
	for _, topic := range []string{"", ".", "..", strings.Repeat("x", 250), "é", "*", "a b", "a\n", "a\x00"} {
		if KafkaTopic(topic) {
			t.Fatal("invalid topic accepted", topic)
		}
	}
}
