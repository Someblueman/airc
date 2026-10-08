//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestKafkaRecordsReplyLossRestartUsesWholeFirstOutcome(t *testing.T) {
	for _, mode := range []string{"expired-send", "history", "history-after-publish-close"} {
		t.Run(mode, func(t *testing.T) {
			f := newKafkaAdmissionFixture(t)
			position, err := f.s.ProvisionKafka(t.Context(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			request := amqpRequest()
			request.RequestID = "original-replayed-intent"
			shift := int64(-400)
			if strings.HasPrefix(mode, "history") {
				request.Operation = core.HistoryRead
				request.Message = ""
				request.Limit = 1
				shift = 0
			}
			record := kafkaSignedRecord(t, f, request, 23, shift)
			var before store.Job
			if mode == "expired-send" {
				old := time.Now().Add(-400 * time.Second)
				verified, err := f.d.AuthenticateDetached(string(record.Value), false, old)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.d.DetachedVerified(t.Context(), verified, old); err != nil {
					t.Fatal(err)
				}
				before, err = f.s.Lookup(t.Context(), f.identity, request.RequestID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.d.AuthenticateDetached(string(record.Value), false, time.Now()); err == nil {
					t.Fatal("fixture is not expired")
				}
			}
			nativeReads := kafkaNativeHistory(t, f, []irc.MessageMetadata{{ID: strings.Repeat("a", 32), From: "native-author", Target: "#fixture", Message: "retained Unicode 世界\npage", Timestamp: time.Unix(1000, 0).UTC()}}, "CHECK=1")
			sealer, pub, _, _ := redisResultFixture(t)
			produced := make(chan []byte, 2)
			fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 24, HighWatermark: 24, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 23, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
			var firstCache []byte
			var first KafkaResult
			for attempt := range 2 {
				p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
					correlation, result := kafkaPeerProducedRecord(t, r, b)
					if len(result.Value) == 0 {
						return
					}
					produced <- result.Value
					if attempt == 0 {
						if mode == "history-after-publish-close" {
							if err := f.s.Close(); err != nil {
								t.Error(err)
								return
							}
							_ = kafkaPeerReply(c, correlation, kafkaProduceSuccess(b))
						}
						return // Applied Produce, with reply loss or journal close before completion.
					}
					_ = kafkaPeerReply(c, correlation, kafkaProduceSuccess(b))
					_, _ = r.ReadByte()
				})
				slots := make(chan struct{}, 1)
				next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, slots, position, fetched)
				p.close()
				if len(slots) != 0 {
					t.Fatal("request capacity leaked")
				}
				if attempt == 0 {
					if mode == "history-after-publish-close" && !errors.Is(err, store.ErrClosed) {
						t.Fatal("completion did not observe closed journal", err)
					}
					if err == nil || next != position {
						t.Fatal("failed completion advanced", next, err)
					}
				} else if err != nil || next.NextOffset != 24 {
					t.Fatal("replay did not complete", next, err)
				}
				scope := kafkaResponseScope(f.c.Realm, b.Route.Principal, f.identity.Binding(), b, 23, request.RequestID)
				plain, err := auth.OpenSignedResponse(string(<-produced), "response", pub, scope, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				var result KafkaResult
				if err := json.Unmarshal(plain, &result); err != nil {
					t.Fatal(err)
				}
				if attempt == 0 {
					first = result
					reopenKafkaRecords(t, &f)
					if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != position {
						t.Fatal("restart lost pending position", current, err)
					}
					admission, err := reserveKafkaRecord(t.Context(), b, f.d, record, position, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					firstCache, err = f.s.KafkaOutcome(t.Context(), admission.reservation.Record)
					if err != nil || firstCache == nil {
						t.Fatal("lost publication lacks outcome", err)
					}
				} else {
					if !reflect.DeepEqual(result, first) {
						t.Fatal("whole first result changed after restart")
					}
					outcome, err := decodeKafkaOutcome(firstCache, request.RequestID, request.Operation)
					if err != nil || !reflect.DeepEqual(outcome.Result, result.Result) {
						t.Fatal("publication replaced first winner", err)
					}
				}
			}
			if mode == "expired-send" {
				after, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
				if err != nil || !reflect.DeepEqual(after, before) || nativeReads.Load() != 0 {
					t.Fatal("retained Send extended or redispatched", after, err)
				}
			} else if nativeReads.Load() != 1 {
				t.Fatal("cached page re-executed", nativeReads.Load())
			}
		})
	}
}
