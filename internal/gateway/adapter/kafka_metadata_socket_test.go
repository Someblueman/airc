package adapter

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func kafkaMetadataWire(host string, port int32) []byte {
	b := []byte{}
	integer := func(n int32) { b = binary.BigEndian.AppendUint32(b, uint32(n)) }
	text := func(s string) { b = binary.BigEndian.AppendUint16(b, uint16(len(s))); b = append(b, s...) }
	integer(0)
	integer(1)
	integer(7)
	text(host)
	integer(port)
	b = append(b, 255, 255)
	text("cluster")
	integer(7)
	integer(2)
	for i, topic := range []string{"events", "results"} {
		b = append(b, 0, 0)
		text(topic)
		b = append(b, 0)
		integer(1)
		b = append(b, 0, 0)
		integer(int32(i + 2))
		integer(7)
		integer(int32(i + 4))
		integer(1)
		integer(7)
		integer(1)
		integer(7)
		integer(0)
		integer(-2147483648)
	}
	integer(-2147483648)
	return b
}

func TestKafkaMetadataSocketScope(t *testing.T) {
	for _, mode := range []string{"valid", "host"} {
		t.Run(mode, func(t *testing.T) {
			// The listener's port is passed only after setup begins, avoiding shared mutable fixture state.
			portReady := make(chan int32, 1)
			done := make(chan struct{})
			rb, security, count := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				defer close(done)
				if !kafkaPeerAuth(t, c, r, "", 0) {
					return
				}
				api, version, correlation, body, err := kafkaPeerRequest(r)
				if err != nil || api != 3 || version != 8 || correlation != 3 || len(body) < 3 || body[len(body)-1] != 0 || body[len(body)-2] != 0 || body[len(body)-3] != 0 {
					t.Error("incorrect scoped metadata command")
					return
				}
				host := "localhost"
				if mode == "host" {
					host = "attacker.example"
				}
				_ = kafkaPeerReply(c, correlation, kafkaMetadataWire(host, <-portReady))
				_, _ = r.ReadByte()
			})
			b, _ := kafkaMetadataPlan()
			b.Brokers[0].Address = rb.Address
			b.Brokers[0].TLSServerName = rb.TLSServerName
			_, port, _ := net.SplitHostPort(rb.Address)
			number, _ := strconv.Atoi(port)
			portReady <- int32(number)
			slots := make(chan struct{}, 1)
			p, err := openKafka(t.Context(), b.Brokers[0], security, config.KafkaCredential{Username: "user"}, []byte("password"), slots)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			leaders, err := p.metadata(b)
			if mode == "valid" {
				if err != nil || leaders.Request.Broker != b.Brokers[0] || leaders.Result.Epoch != 5 {
					t.Fatal("metadata selection", err)
				}
			} else if err != errKafkaConnection || leaders != (kafkaLeaders{}) || len(slots) != 0 {
				t.Fatal("untrusted endpoint retained socket")
			}
			p.close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("metadata close did not join peer")
			}
			if count.Load() != 1 {
				t.Fatal("advertised endpoint caused dialing")
			}
		})
	}
}
