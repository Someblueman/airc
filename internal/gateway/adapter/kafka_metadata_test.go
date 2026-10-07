package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

func kafkaMetadataPlan() (config.Kafka, kafkawire.MetadataResult) {
	b := config.Kafka{ClusterID: "cluster", Brokers: []config.KafkaBroker{{ID: 7, Address: "127.0.0.1:9093", TLSServerName: "broker.example"}}, Route: config.KafkaRoute{Topic: "events", Partition: 2, ResultTopic: "results", ResultPartition: 3}}
	m := kafkawire.MetadataResult{ClusterID: "cluster", Brokers: []kafkawire.Broker{{ID: 7, Host: "broker.example", Port: 9093}}, Partitions: []kafkawire.Partition{{Topic: "events", Index: 2, Leader: 7, LeaderEpoch: 4}, {Topic: "results", Index: 3, Leader: 7, LeaderEpoch: 5}}}
	return b, m
}
func TestKafkaMetadataPinnedLeaders(t *testing.T) {
	b, m := kafkaMetadataPlan()
	leaders, err := kafkaSelectLeaders(b, m)
	if err != nil || leaders.Request.Broker != b.Brokers[0] || leaders.Request.Epoch != 4 || leaders.Result.Epoch != 5 {
		t.Fatal("pinned selection", err)
	}
	for name, mutate := range map[string]func(*config.Kafka, *kafkawire.MetadataResult){
		"cluster":        func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.ClusterID = "other" },
		"unknown_id":     func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Brokers[0].ID = 8 },
		"host":           func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Brokers[0].Host = "attacker.example" },
		"port":           func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Brokers[0].Port = 9094 },
		"ip":             func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Brokers[0].Host = "127.0.0.2" },
		"leader":         func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Partitions[0].Leader = 8 },
		"partition":      func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Partitions[0].Index = 0 },
		"epoch":          func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Partitions[0].LeaderEpoch = -1 },
		"missing_result": func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Partitions = m.Partitions[:1] },
		"duplicate":      func(_ *config.Kafka, m *kafkawire.MetadataResult) { m.Brokers = append(m.Brokers, m.Brokers[0]) },
		"duplicate_partition": func(_ *config.Kafka, m *kafkawire.MetadataResult) {
			m.Partitions = append(m.Partitions, m.Partitions[0])
		},
		"dns_dial": func(b *config.Kafka, _ *kafkawire.MetadataResult) { b.Brokers[0].Address = "broker.example:9093" },
	} {
		t.Run(name, func(t *testing.T) {
			b, m := kafkaMetadataPlan()
			mutate(&b, &m)
			if got, e := kafkaSelectLeaders(b, m); e != errKafkaConnection || got != (kafkaLeaders{}) {
				t.Fatal("unsafe metadata accepted")
			}
		})
	}
	for _, host := range []string{"BROKER.EXAMPLE.", "127.0.0.1"} {
		b, m := kafkaMetadataPlan()
		m.Brokers[0].Host = host
		if _, err := kafkaSelectLeaders(b, m); err != nil {
			t.Fatal("equivalent host refused", err)
		}
	}
	b, m = kafkaMetadataPlan()
	b.Brokers[0].Address = "[::1]:9093"
	m.Brokers[0].Host = "0:0:0:0:0:0:0:1"
	if _, err := kafkaSelectLeaders(b, m); err != nil {
		t.Fatal("IPv6 spelling", err)
	}
}
