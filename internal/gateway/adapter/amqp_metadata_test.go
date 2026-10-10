package adapter

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestAMQPMetadataAuthorityEncodingAndExpiry(t *testing.T) {
	now := time.Now()
	arrived := now.Add(-time.Second)
	route := config.AMQPRoute{RequestAddress: "request", ResponseAddress: "response"}
	props := []amqpwire.Value{{Code: 0xa1, Data: []byte("wire-id")}, {Code: 0xa0, Data: []byte("producer")}, {Code: 0xa1, Data: []byte("request")}, {Code: 0x40}, {Code: 0xa1, Data: []byte("response")}, {Code: 0xa1, Data: []byte("wire-correlation")}, {Code: 0xa3, Data: []byte("application/json")}, {Code: 0x40}, {Code: 0x83, Number: uint64(now.Add(2 * time.Second).UnixMilli())}}
	message := amqpwire.Message{Properties: props, Header: []amqpwire.Value{{Code: 0x40}, {Code: 0x40}, {Code: 0x70, Number: 1500}}}
	deadline, c := amqpMetadata(message, "producer", route, arrived, now)
	if c != "" || !deadline.Equal(now.Add(500*time.Millisecond)) {
		t.Fatal(deadline, c)
	}
	for _, tc := range []struct {
		index int
		value amqpwire.Value
		want  string
	}{
		{1, amqpwire.Value{Code: 0xa0, Data: []byte("spoofed")}, "amqp:unauthorized-access"},
		{2, amqpwire.Value{Code: 0xa1, Data: []byte("other-request")}, "amqp:unauthorized-access"},
		{4, amqpwire.Value{Code: 0xa1, Data: []byte("other-response")}, "amqp:unauthorized-access"},
		{6, amqpwire.Value{Code: 0xa3, Data: []byte("application/jose")}, "amqp:not-implemented"},
		{7, amqpwire.Value{Code: 0xa3, Data: []byte("gzip")}, "amqp:not-implemented"},
		{7, amqpwire.Value{Code: 0xa3, Data: []byte("identity")}, "amqp:not-implemented"},
		{8, amqpwire.Value{Code: 0x83, Number: uint64(now.UnixMilli())}, "amqp:precondition-failed"},
		{8, amqpwire.Value{Code: 0x83, Number: ^uint64(0)}, "amqp:precondition-failed"},
	} {
		copy := message
		copy.Properties = append([]amqpwire.Value{}, message.Properties...)
		copy.Properties[tc.index] = tc.value
		if _, c := amqpMetadata(copy, "producer", route, arrived, now); c != tc.want {
			t.Fatal(tc.index, c)
		}
	}
	for _, ttl := range []uint64{0, 1000} {
		message.Header[2].Number = ttl
		if _, c := amqpMetadata(message, "producer", route, arrived, now); c != "amqp:precondition-failed" {
			t.Fatal(ttl, c)
		}
	}
	message.Header = nil
	message.Properties = props[:8]
	if deadline, c := amqpMetadata(message, "producer", route, arrived, now); c != "" || !deadline.Equal(now.Add(amqpDeadline)) {
		t.Fatal(deadline, c)
	}
	message.Properties = props
	message.Properties[8].Number = uint64(now.Add(250 * time.Millisecond).UnixMilli())
	if deadline, c := amqpMetadata(message, "producer", route, arrived, now); c != "" || deadline.UnixMilli() != int64(message.Properties[8].Number) {
		t.Fatal(deadline, c)
	}
}
