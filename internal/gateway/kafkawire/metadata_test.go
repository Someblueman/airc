package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func metadataReply() ([]byte, map[string]int) {
	body := []byte{}
	positions := map[string]int{}
	i32 := func(name string, value int32) {
		positions[name] = len(body)
		body = binary.BigEndian.AppendUint32(body, uint32(value))
	}
	text := func(name, value string) { positions[name] = len(body); body = appendText(body, value) }
	i32("throttle", 0)
	i32("brokers", 1)
	i32("broker", 7)
	text("host", "broker.example")
	i32("port", 9093)
	body = binary.BigEndian.AppendUint16(body, 0xffff)
	text("cluster", "cluster")
	i32("controller", 7)
	i32("topics", 1)
	positions["topic_error"] = len(body)
	body = append(body, 0, 0)
	text("topic", "events")
	positions["internal"] = len(body)
	body = append(body, 0)
	i32("partitions", 1)
	positions["partition_error"] = len(body)
	body = append(body, 0, 0)
	i32("index", 2)
	i32("leader", 7)
	i32("epoch", 4)
	i32("replicas", 1)
	i32("replica", 7)
	i32("isrs", 1)
	i32("isr", 7)
	i32("offline", 0)
	i32("topic_ops", -2147483648)
	i32("cluster_ops", -2147483648)
	return body, positions
}

func TestKafkaMetadataExactRequestAndOwnedReply(t *testing.T) {
	body, err := Metadata([]string{"events", "results"})
	expected := []byte{0, 0, 0, 2, 0, 6, 'e', 'v', 'e', 'n', 't', 's', 0, 7, 'r', 'e', 's', 'u', 'l', 't', 's', 0, 0, 0}
	if err != nil || !bytes.Equal(body, expected) {
		t.Fatal("request", body, err)
	}
	wire, _ := metadataReply()
	got, err := MetadataReply(wire, []string{"events"}, "cluster")
	if err != nil || got.ClusterID != "cluster" || len(got.Brokers) != 1 || got.Brokers[0] != (Broker{ID: 7, Host: "broker.example", Port: 9093}) || len(got.Partitions) != 1 || got.Partitions[0] != (Partition{Topic: "events", Index: 2, Leader: 7, LeaderEpoch: 4}) {
		t.Fatal("reply", got, err)
	}
	clear(wire)
	if got.Brokers[0].Host != "broker.example" || got.Partitions[0].Topic != "events" {
		t.Fatal("reply aliases input")
	}
	for _, topics := range [][]string{nil, {}, {"events", "events"}, {"events", "results", "other"}, {"bad topic"}} {
		if body, err := Metadata(topics); !errors.Is(err, ErrFrame) || body != nil {
			t.Fatal("bad request", topics)
		}
	}
}

func TestKafkaMetadataRefusalBoundaries(t *testing.T) {
	valid, positions := metadataReply()
	for n := 0; n < len(valid); n++ {
		if got, err := MetadataReply(valid[:n], []string{"events"}, "cluster"); !errors.Is(err, ErrFrame) || len(got.Brokers) != 0 || len(got.Partitions) != 0 {
			t.Fatal("truncated", n)
		}
	}
	for name, value := range map[string]int32{"throttle": -1, "brokers": 33, "broker": -1, "port": 65536, "controller": 9, "topics": 2, "partitions": 257, "index": -1, "leader": 9, "epoch": -1, "replicas": 33, "replica": 9, "isrs": 0, "isr": 9, "offline": 33} {
		body := bytes.Clone(valid)
		binary.BigEndian.PutUint32(body[positions[name]:], uint32(value))
		if _, err := MetadataReply(body, []string{"events"}, "cluster"); !errors.Is(err, ErrFrame) {
			t.Fatal("invalid field", name)
		}
	}
	for _, name := range []string{"topic_error", "partition_error", "internal"} {
		body := bytes.Clone(valid)
		body[positions[name]] = 1
		if _, err := MetadataReply(body, []string{"events"}, "cluster"); !errors.Is(err, ErrFrame) {
			t.Fatal("error flag", name)
		}
	}
	for _, scope := range []struct{ topic, cluster string }{{"other", "cluster"}, {"events", "other"}, {"events", ""}} {
		if _, err := MetadataReply(valid, []string{scope.topic}, scope.cluster); !errors.Is(err, ErrFrame) {
			t.Fatal("wrong scope")
		}
	}
	if _, err := MetadataReply(append(valid, 0), []string{"events"}, "cluster"); !errors.Is(err, ErrFrame) {
		t.Fatal("trailing")
	}
	body := bytes.Clone(valid)
	body[positions["host"]+2] = 255
	if _, err := MetadataReply(body, []string{"events"}, "cluster"); !errors.Is(err, ErrFrame) {
		t.Fatal("invalid host UTF8")
	}
}
