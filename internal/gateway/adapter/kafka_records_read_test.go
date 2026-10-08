//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestKafkaRecordsFreshWholeReadAndCapacityPreflight(t *testing.T) {
	for _, oversize := range []bool{false, true} {
		t.Run(fmt.Sprint(oversize), func(t *testing.T) {
			f := newKafkaAdmissionFixture(t)
			position, err := f.s.ProvisionKafka(t.Context(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			count := 1
			if oversize {
				count = 6
			}
			var messages []irc.MessageMetadata
			for i := range count {
				body := "exact Unicode 世界\nsecond line"
				if oversize {
					body = strings.Repeat("x", 4096)
				}
				messages = append(messages, irc.MessageMetadata{ID: fmt.Sprintf("%032x", i+1), From: "native-author", Target: "#fixture", Message: body, Timestamp: time.Unix(1000, 0).UTC()})
			}
			reads := kafkaNativeHistory(t, f, messages, "CHECK=1")
			request := core.Request{Version: 1, RequestID: "whole-read", Operation: core.HistoryRead, Target: amqpRequest().Target, Limit: count}
			record := kafkaSignedRecord(t, f, request, 23, 0)
			admission, err := reserveKafkaRecord(t.Context(), f.c.Kafka[0], f.d, record, position, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			produced := make(chan []byte, 1)
			p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
				correlation, result := kafkaPeerProducedRecord(t, r, b)
				if len(result.Value) == 0 {
					return
				}
				produced <- result.Value
				_ = kafkaPeerReply(c, correlation, kafkaProduceSuccess(b))
				_, _ = r.ReadByte()
			})
			sealer, pub, _, _ := redisResultFixture(t)
			slots := make(chan struct{}, 1)
			fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 24, HighWatermark: 24, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 23, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
			next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, slots, position, fetched)
			if oversize {
				if !errors.Is(err, auth.ErrResponseCapacity) || next != position {
					t.Fatal("oversize completed", next, err)
				}
			} else if err != nil || next.NextOffset != 24 {
				t.Fatal("whole read failed", next, err)
			}
			p.close()
			scope := kafkaResponseScope(f.c.Realm, b.Route.Principal, f.identity.Binding(), b, 23, request.RequestID)
			plain, err := auth.OpenSignedResponse(string(<-produced), "response", pub, scope, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var response KafkaResult
			if err := json.Unmarshal(plain, &response); err != nil {
				t.Fatal(err)
			}
			cache, err := f.s.KafkaOutcome(t.Context(), admission.reservation.Record)
			if err != nil {
				t.Fatal(err)
			}
			if oversize {
				if cache != nil || response.Result != nil || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient {
					t.Fatal("partial outcome cached or failure omitted", response)
				}
			} else {
				if cache == nil || response.Result == nil || response.Result.Page == nil || response.Result.Page.Cursor == "" || response.Result.Page.Status != "more" || !response.Result.Page.Gap || len(response.Result.Page.Messages) != len(messages) {
					t.Fatal("whole page changed", response)
				}
				for i, m := range messages {
					if !reflect.DeepEqual(*response.Result.Page.Messages[i], m) {
						t.Fatal("metadata shortened")
					}
				}
			}
			if reads.Load() != 1 || len(slots) != 0 {
				t.Fatal("read repeated or capacity leaked", reads.Load())
			}
		})
	}
}
