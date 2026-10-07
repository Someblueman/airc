package config

import (
	"fmt"
	"slices"
	"testing"
)

func TestKafkaOwnerAndBrokerBounds(t *testing.T) {
	for _, count := range []int{32, 33} {
		c := kafkaConfig()
		b := c.Kafka[0]
		c.Kafka = nil
		for i := range count {
			b.ID = fmt.Sprintf("owner%d", i)
			b.Route.Partition = int32(i)
			b.Route.ResultPartition = int32(i)
			c.Kafka = append(c.Kafka, b)
		}
		if err := c.Validate(); (err == nil) != (count == 32) {
			t.Fatalf("owners %d: %v", count, err)
		}
		c = kafkaConfig()
		c.Kafka[0].Brokers = nil
		for i := range count {
			c.Kafka[0].Brokers = append(c.Kafka[0].Brokers, KafkaBroker{ID: int32(i), Address: fmt.Sprintf("127.0.0.1:%d", 9093+i), TLSServerName: "localhost"})
		}
		if err := c.Validate(); (err == nil) != (count == 32) {
			t.Fatalf("brokers %d: %v", count, err)
		}
	}
	c := kafkaConfig()
	c.Kafka[0].Brokers = append(c.Kafka[0].Brokers, KafkaBroker{ID: 1, Address: "127.0.0.1:9094", TLSServerName: "localhost"})
	b := c.Kafka[0]
	b.ID = "second"
	b.Route.Partition = 1
	b.Route.ResultPartition = 1
	b.Brokers = slices.Clone(b.Brokers)
	slices.Reverse(b.Brokers)
	c.Kafka = append(c.Kafka, b)
	if err := c.Validate(); err != nil {
		t.Fatalf("allowlist order: %v", err)
	}
	c.Kafka[1].ClusterID = "other"
	c.Kafka[1].Namespace = "other"
	c.Kafka[1].Brokers = []KafkaBroker{{ID: 0, Address: "127.0.0.1:10000", TLSServerName: "localhost"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("distinct cluster: %v", err)
	}
	c.Kafka[1].Brokers[0].Address = c.Kafka[0].Brokers[0].Address
	if c.Validate() == nil {
		t.Fatal("endpoint shared across cluster identities")
	}
}
