package adapter

import (
	"bufio"
	"crypto/tls"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestKafkaPairLeaderConnectionAndChange(t *testing.T) {
	for _, mode := range []string{"colocated", "split", "changed", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			ready := make(chan struct{})
			b, _ := kafkaMetadataPlan()
			b.Credential = config.KafkaCredential{Username: "user"}
			handler := func(resultPeer bool) func(int, *tls.Conn, *bufio.Reader) {
				return func(_ int, c *tls.Conn, r *bufio.Reader) {
					<-ready
					if !kafkaPeerAuth(t, c, r, "", 0) {
						return
					}
					api, version, correlation, _, err := kafkaPeerRequest(r)
					if err != nil || api != 3 || version != 8 || correlation != 3 {
						t.Error("leader metadata refresh failed")
						return
					}
					result := int32(7)
					if mode != "colocated" {
						result = 8
					}
					epoch := int32(4)
					if mode == "changed" && resultPeer {
						epoch = 5
					}
					_ = kafkaPeerReply(c, correlation, kafkaPairMetadataWire(b, 7, result, epoch))
					_, _ = r.ReadByte()
				}
			}
			rb, security, count := redisFixture(t, handler(false))
			b.Brokers = []config.KafkaBroker{{ID: 7, Address: rb.Address, TLSServerName: rb.TLSServerName}}
			securityMap := map[int32]*tls.Config{7: security}
			seed := 0
			if mode != "colocated" {
				rb, s, resultCount := redisFixture(t, handler(true))
				b.Brokers = append(b.Brokers, config.KafkaBroker{ID: 8, Address: rb.Address, TLSServerName: rb.TLSServerName})
				securityMap[8] = s
				_ = resultCount
			}
			// A result-leader bootstrap is closed before opening the request leader.
			if mode == "split" {
				seed = 1
			}
			close(ready)
			capacity := 2
			if mode == "capacity" {
				capacity = 1
			}
			slots := make(chan struct{}, capacity)
			p, err := openKafkaPair(t.Context(), b, securityMap, []byte("password"), slots, seed)
			if mode == "changed" || mode == "capacity" {
				if err != errKafkaConnection || p != nil || len(slots) != 0 {
					t.Fatal("changed epoch retained pair")
				}
				return
			}
			if err != nil || p == nil {
				t.Fatal("pair setup", err)
			}
			defer p.close()
			if mode == "colocated" {
				if p.request != p.result || len(slots) != 1 || count.Load() != 1 {
					t.Fatal("colocated leaders did not share socket")
				}
			} else if p.request == p.result || len(slots) != 2 || p.leaders.Result.Broker.ID != 8 {
				t.Fatal("split leaders incorrectly owned")
			}
			p.close()
			p.close()
			if len(slots) != 0 {
				t.Fatal("pair close leaked capacity")
			}
		})
	}
}
