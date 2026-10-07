package kafkawire

import (
	"encoding/binary"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func metadataTopics(topics []string) bool {
	if len(topics) < 1 || len(topics) > 2 {
		return false
	}
	for i, topic := range topics {
		if !config.KafkaTopic(topic) || slices.Contains(topics[:i], topic) {
			return false
		}
	}
	return true
}

// Metadata encodes v8 for explicit topics, disabling automatic topic creation
// and authorized-operation listings. It never asks for all cluster topics.
func Metadata(topics []string) ([]byte, error) {
	if !metadataTopics(topics) {
		return nil, ErrFrame
	}
	body := binary.BigEndian.AppendUint32(nil, uint32(len(topics)))
	for _, topic := range topics {
		body = appendText(body, topic)
	}
	return append(body, 0, 0, 0), nil
}

type Broker struct {
	ID   int32
	Host string
	Port int32
}
type Partition struct {
	Topic                      string
	Index, Leader, LeaderEpoch int32
}
type MetadataResult struct {
	ClusterID  string
	Brokers    []Broker
	Partitions []Partition
}

// MetadataReply validates a complete bounded v8 response for exactly the requested
// topics and cluster. Advertised brokers are information, never dial authority;
// the owner must resolve their IDs through its configured verified endpoint map.
func MetadataReply(body []byte, topics []string, cluster string) (MetadataResult, error) {
	if len(body) > MaxFrame || !metadataTopics(topics) || len(cluster) < 1 || len(cluster) > 255 {
		return MetadataResult{}, ErrFrame
	}
	d := decoder{body: body}
	throttle := d.i32()
	count := d.i32()
	if throttle < 0 || count < 1 || count > 32 {
		return MetadataResult{}, ErrFrame
	}
	result := MetadataResult{}
	brokers := map[int32]bool{}
	for range int(count) {
		id := d.i32()
		host := d.text(255, false)
		port := d.i32()
		_ = d.text(255, true)
		if d.bad || id < 0 || brokers[id] || host == "" || strings.ContainsAny(host, "\x00\r\n") || port < 1 || port > 65535 {
			return MetadataResult{}, ErrFrame
		}
		brokers[id] = true
		result.Brokers = append(result.Brokers, Broker{ID: id, Host: host, Port: port})
	}
	result.ClusterID = d.text(255, true)
	controller := d.i32()
	topicCount := d.i32()
	if d.bad || result.ClusterID != cluster || controller < -1 || controller >= 0 && !brokers[controller] || topicCount != int32(len(topics)) {
		return MetadataResult{}, ErrFrame
	}
	seen := map[string]bool{}
	total := 0
	for range int(topicCount) {
		code := d.i16()
		topic := d.text(249, false)
		internal := d.take(1)
		partitions := d.i32()
		if d.bad || code != 0 || !slices.Contains(topics, topic) || seen[topic] || len(internal) != 1 || internal[0] != 0 || partitions < 1 || partitions > 256 || total+int(partitions) > 256 {
			return MetadataResult{}, ErrFrame
		}
		seen[topic] = true
		total += int(partitions)
		indices := map[int32]bool{}
		for range int(partitions) {
			code := d.i16()
			index := d.i32()
			leader := d.i32()
			epoch := d.i32()
			if d.bad || code != 0 || index < 0 || indices[index] || !brokers[leader] || epoch < 0 {
				return MetadataResult{}, ErrFrame
			}
			indices[index] = true
			replicas := metadataNodes(&d, brokers)
			isr := metadataNodes(&d, brokers)
			offline := metadataNodes(&d, brokers)
			if d.bad || len(replicas) == 0 || !slices.Contains(replicas, leader) || !slices.Contains(isr, leader) {
				return MetadataResult{}, ErrFrame
			}
			for _, id := range append(isr, offline...) {
				if !slices.Contains(replicas, id) {
					return MetadataResult{}, ErrFrame
				}
			}
			result.Partitions = append(result.Partitions, Partition{Topic: topic, Index: index, Leader: leader, LeaderEpoch: epoch})
		}
		_ = d.i32() // Topic authorized operations remain a broker bit field.
	}
	_ = d.i32() // Cluster authorized operations.
	if !d.done() {
		return MetadataResult{}, ErrFrame
	}
	return result, nil
}

func metadataNodes(d *decoder, brokers map[int32]bool) []int32 {
	count := d.i32()
	if d.bad || count < 0 || count > 32 {
		d.bad = true
		return nil
	}
	nodes := make([]int32, 0, int(count))
	for range int(count) {
		id := d.i32()
		if d.bad || !brokers[id] || slices.Contains(nodes, id) {
			d.bad = true
			return nil
		}
		nodes = append(nodes, id)
	}
	return nodes
}
