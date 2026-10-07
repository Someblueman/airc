package adapter

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

type kafkaLeader struct {
	Broker config.KafkaBroker
	Epoch  int32
}
type kafkaLeaders struct{ Request, Result kafkaLeader }

// metadata never dials an advertised address. Every broker must match a pinned
// ID and endpoint; leaders are selected only from the configured partition pair.
func (p *kafkaConn) metadata(b config.Kafka) (kafkaLeaders, error) {
	topics := []string{b.Route.Topic, b.Route.ResultTopic}
	body, err := kafkawire.Metadata(topics)
	if err == nil {
		var reply []byte
		reply, err = p.command(3, 8, body, time.Time{})
		if err == nil {
			var result kafkawire.MetadataResult
			result, err = kafkawire.MetadataReply(reply, topics, b.ClusterID)
			if err == nil {
				if leaders, e := kafkaSelectLeaders(b, result); e == nil {
					return leaders, nil
				}
			}
		}
	}
	p.close()
	return kafkaLeaders{}, errKafkaConnection
}

func kafkaSelectLeaders(b config.Kafka, m kafkawire.MetadataResult) (kafkaLeaders, error) {
	if m.ClusterID != b.ClusterID || len(b.Brokers) < 1 || len(b.Brokers) > 32 || b.Route.Topic == b.Route.ResultTopic || b.Route.Partition < 0 || b.Route.ResultPartition < 0 {
		return kafkaLeaders{}, errKafkaConnection
	}
	configured := map[int32]config.KafkaBroker{}
	for _, broker := range b.Brokers {
		if broker.ID < 0 {
			return kafkaLeaders{}, errKafkaConnection
		}
		if _, ok := configured[broker.ID]; ok {
			return kafkaLeaders{}, errKafkaConnection
		}
		configured[broker.ID] = broker
	}
	advertised := map[int32]bool{}
	for _, broker := range m.Brokers {
		pinned, ok := configured[broker.ID]
		if !ok || advertised[broker.ID] || !kafkaEndpointMatches(pinned, broker) {
			return kafkaLeaders{}, errKafkaConnection
		}
		advertised[broker.ID] = true
	}
	var leaders kafkaLeaders
	request, result := false, false
	for _, partition := range m.Partitions {
		if !advertised[partition.Leader] || partition.LeaderEpoch < 0 {
			return kafkaLeaders{}, errKafkaConnection
		}
		leader := kafkaLeader{Broker: configured[partition.Leader], Epoch: partition.LeaderEpoch}
		if partition.Topic == b.Route.Topic && partition.Index == b.Route.Partition {
			if request {
				return kafkaLeaders{}, errKafkaConnection
			}
			leaders.Request = leader
			request = true
		}
		if partition.Topic == b.Route.ResultTopic && partition.Index == b.Route.ResultPartition {
			if result {
				return kafkaLeaders{}, errKafkaConnection
			}
			leaders.Result = leader
			result = true
		}
	}
	if !request || !result {
		return kafkaLeaders{}, errKafkaConnection
	}
	return leaders, nil
}

func kafkaEndpointMatches(pinned config.KafkaBroker, advertised kafkawire.Broker) bool {
	host, port, err := net.SplitHostPort(pinned.Address)
	number, portErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || portErr != nil || ip == nil || ip.IsUnspecified() || number < 1 || number > 65535 || advertised.Port != int32(number) {
		return false
	}
	if other := net.ParseIP(advertised.Host); other != nil {
		return ip.Equal(other)
	}
	return pinned.TLSServerName != "" && strings.EqualFold(strings.TrimSuffix(advertised.Host, "."), strings.TrimSuffix(pinned.TLSServerName, "."))
}
