package adapter

import (
	"errors"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

const kafkaDeadline = 3 * time.Second

var errKafkaConnection = errors.New("kafka connection refused")

// kafkaCommand is a serial exchange on an already verified TLS connection.
// The socket owner closes the raw connection on cancellation or any error and
// joins cancellation before releasing capacity. Broker text is never exposed.
func kafkaCommand(c net.Conn, api, version int16, correlation int32, body []byte, deadline time.Time) ([]byte, error) {
	wire, err := kafkawire.Request(api, version, correlation, body)
	if err != nil {
		return nil, errKafkaConnection
	}
	defer clear(wire) // The authentication command includes the broker password.
	deadline = stompEarlier(time.Now().Add(kafkaDeadline), deadline)
	if c == nil || !time.Now().Before(deadline) || c.SetDeadline(deadline) != nil {
		return nil, errKafkaConnection
	}
	for rest := wire; len(rest) > 0; {
		n, err := c.Write(rest)
		if err != nil || n <= 0 || n > len(rest) || !time.Now().Before(deadline) {
			return nil, errKafkaConnection
		}
		rest = rest[n:]
	}
	reply, err := kafkawire.Response(c, correlation)
	if err != nil || !time.Now().Before(deadline) {
		return nil, errKafkaConnection
	}
	return reply, nil
}
