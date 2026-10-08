//go:build darwin || linux

package service

import (
	"encoding/binary"
	"net"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func kafkaServiceMetadataWire(b config.Kafka, request, result int32, epoch int32) []byte {
	wire := []byte{}
	integer := func(n int32) { wire = binary.BigEndian.AppendUint32(wire, uint32(n)) }
	text := func(s string) { wire = binary.BigEndian.AppendUint16(wire, uint16(len(s))); wire = append(wire, s...) }
	integer(0)
	integer(int32(len(b.Brokers)))
	for _, broker := range b.Brokers {
		_, port, _ := net.SplitHostPort(broker.Address)
		number, _ := strconv.Atoi(port)
		integer(broker.ID)
		text(broker.TLSServerName)
		integer(int32(number))
		wire = append(wire, 255, 255)
	}
	text(b.ClusterID)
	integer(b.Brokers[0].ID)
	integer(2)
	for i, topic := range []string{b.Route.Topic, b.Route.ResultTopic} {
		leader, index := request, b.Route.Partition
		if i == 1 {
			leader, index = result, b.Route.ResultPartition
		}
		wire = append(wire, 0, 0)
		text(topic)
		wire = append(wire, 0)
		integer(1)
		wire = append(wire, 0, 0)
		integer(index)
		integer(leader)
		integer(epoch)
		integer(1)
		integer(leader)
		integer(1)
		integer(leader)
		integer(0)
		integer(-2147483648)
	}
	integer(-2147483648)
	return wire
}
