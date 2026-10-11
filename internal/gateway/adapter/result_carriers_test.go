package adapter

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
	"github.com/Someblueman/airc/pkg/irc"
)

// resultEnvelope is the plain JSON result every broker carrier publishes:
// a client needs only a JSON parser, no signature or key.
type resultEnvelope struct {
	Version   int                      `json:"version"`
	Protocol  string                   `json:"protocol"`
	RequestID string                   `json:"request_id"`
	Result    *dispatch.DetachedResult `json:"result"`
	Error     *core.Failure            `json:"error"`
	fields    map[string]any
}

// resultCarrier marshals results for one broker carrier's route.
type resultCarrier struct {
	name, protocol string
	// limit is the carrier's message bound; frameErr and capacityErr are its
	// refusals of a malformed and an oversized result.
	limit                 int
	frameErr, capacityErr error
	// names are the route's own names, echoed literally in every result.
	names map[string]any
	// shapes: the carrier refuses results whose shape does not fit the
	// operation, not only a missing or doubled result.
	shapes bool
	encode func(requestID string, r *dispatch.DetachedResult, f *core.Failure) ([]byte, error)
	body   func(t *testing.T, wire []byte) []byte
}

func resultCarriers() []resultCarrier {
	plain := func(_ *testing.T, wire []byte) []byte { return wire }
	amqpRoute := config.AMQPRoute{RequestAddress: "requests/a:send", ResponseAddress: "responses/a:read"}
	stompRoute := config.STOMPRoute{RequestDestination: "requests/a:send", ResponseDestination: "responses/a:read"}
	redisRoute := redisResultRoute()
	streams := redisStreamResultBroker()
	target := map[string]any{"kind": "room", "name": "#fixture"}
	return []resultCarrier{
		{
			name: "amqp", protocol: "amqp/1", limit: amqpwire.MaxMessageBody, frameErr: amqpwire.ErrValue, capacityErr: errResultCapacity,
			names: map[string]any{"request_address": amqpRoute.RequestAddress, "response_address": amqpRoute.ResponseAddress},
			encode: func(id string, r *dispatch.DetachedResult, f *core.Failure) ([]byte, error) {
				return amqpResultMessage(amqpRoute, id, r, f)
			},
			body: func(t *testing.T, wire []byte) []byte {
				t.Helper()
				message, err := amqpwire.DecodeMessage(wire)
				if err != nil {
					t.Fatal(err)
				}
				return message.Body
			},
		},
		{
			name: "stomp", protocol: "stomp/1.2", limit: stompwire.MaxBody, frameErr: stompwire.ErrFrame, capacityErr: errResultCapacity,
			names: map[string]any{"request_destination": stompRoute.RequestDestination, "response_destination": stompRoute.ResponseDestination},
			encode: func(id string, r *dispatch.DetachedResult, f *core.Failure) ([]byte, error) {
				return stompResultMessage(stompRoute, id, r, f)
			},
			body: plain,
		},
		{
			name: "redis-pubsub", protocol: "redis/pubsub", limit: rediswire.MaxBulk, frameErr: rediswire.ErrFrame, capacityErr: errRedisResultCapacity, shapes: true,
			names: map[string]any{"request_channel": redisRoute.RequestChannel, "response_channel": redisRoute.ResponseChannel, "target": target},
			encode: func(id string, r *dispatch.DetachedResult, f *core.Failure) ([]byte, error) {
				return redisResultMessage(redisRoute, id, r, f)
			},
			body: plain,
		},
		{
			name: "redis-streams", protocol: "redis/streams", limit: rediswire.MaxBulk, frameErr: rediswire.ErrFrame, capacityErr: errRedisResultCapacity, shapes: true,
			names: map[string]any{"namespace": streams.Namespace, "stream": streams.Route.Stream, "result_stream": streams.Route.ResultStream, "entry_id": "1760140800123-7", "target": target},
			encode: func(id string, r *dispatch.DetachedResult, f *core.Failure) ([]byte, error) {
				return redisStreamResultMessage(streams, "1760140800123-7", id, r, f)
			},
			body: plain,
		},
	}
}

func (c resultCarrier) decode(t *testing.T, wire []byte) resultEnvelope {
	t.Helper()
	body := c.body(t, wire)
	var e resultEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("result is not plain JSON: %s: %v", body, err)
	}
	if err := json.Unmarshal(body, &e.fields); err != nil {
		t.Fatal(err)
	}
	if e.Version != 1 || e.Protocol != c.protocol {
		t.Fatalf("envelope: %s", body)
	}
	for k, v := range c.names {
		if !reflect.DeepEqual(e.fields[k], v) {
			t.Fatalf("route name %s changed: %s", k, body)
		}
	}
	return e
}

// TestCarrierResults exercises result marshalling once per broker carrier: a
// durable result and a failure are correlated by the request's own ID, reads
// keep their whole page or context, an oversized result is refused without
// truncating it, and a malformed result is never published.
func TestCarrierResults(t *testing.T) {
	for _, c := range resultCarriers() {
		t.Run(c.name, func(t *testing.T) {
			t.Run("durable", func(t *testing.T) {
				result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "stable-inner-id", State: core.Queued, Durable: true}}
				wire, err := c.encode("stable-inner-id", &result, nil)
				if err != nil {
					t.Fatal(err)
				}
				e := c.decode(t, wire)
				if e.RequestID != "stable-inner-id" || e.Error != nil || e.Result == nil || e.Result.Operation != core.Send || e.Result.Result.RequestID != "stable-inner-id" || !e.Result.Result.Durable || e.Result.Result.Accepted != nil {
					t.Fatalf("durable result: %+v", e)
				}
			})
			t.Run("failure", func(t *testing.T) {
				wire, err := c.encode("failed-id", nil, &core.Failure{Code: core.IDConflict})
				if err != nil {
					t.Fatal(err)
				}
				if e := c.decode(t, wire); e.RequestID != "failed-id" || e.Error == nil || e.Error.Code != core.IDConflict || e.Result != nil {
					t.Fatalf("failure correlation: %+v", e)
				}
			})
			t.Run("reads", func(t *testing.T) {
				message := irc.MessageMetadata{ID: "message-id", From: "author", Target: "#fixture", Message: "exact Unicode 世界\nsecond line", Timestamp: time.Unix(1000, 0).UTC()}
				for _, result := range []dispatch.DetachedResult{
					{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-resume-token", Status: "more", Gap: true}},
					{Version: 1, Operation: core.Search, Page: &backend.HistoryPage{Messages: []*irc.MessageMetadata{&message}, Cursor: "opaque-search-token", Status: "ok", Gap: false}},
					{Version: 1, Operation: core.ContextRead, Context: &core.ContextResult{Summary: irc.ContextSummary{RootID: "root", TriggerID: "trigger", Missing: []string{"missing"}, OmittedMessages: 2}, Messages: []irc.MessageMetadata{message}}},
				} {
					wire, err := c.encode("read-id", &result, nil)
					if err != nil {
						t.Fatal(err)
					}
					if e := c.decode(t, wire); e.RequestID != "read-id" || e.Result == nil || !reflect.DeepEqual(*e.Result, result) {
						t.Fatalf("read boundary changed for %s", result.Operation)
					}
				}
			})
			t.Run("capacity", func(t *testing.T) {
				result := dispatch.DetachedResult{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Cursor: strings.Repeat("c", c.limit), Status: "more", Gap: true}}
				if b, err := c.encode("id", &result, nil); !errors.Is(err, c.capacityErr) || b != nil {
					t.Fatal("oversize page emitted", err)
				}
				if len(result.Page.Cursor) != c.limit || result.Page.Status != "more" || !result.Page.Gap {
					t.Fatal("page/cursor truncated")
				}
				// The client still gets a correlated, transient failure.
				wire, err := c.encode("id", nil, &core.Failure{Code: core.CapacityExceeded, Transient: true})
				if err != nil {
					t.Fatal(err)
				}
				if e := c.decode(t, wire); len(wire) > c.limit || e.Error == nil || e.Error.Code != core.CapacityExceeded || !e.Error.Transient {
					t.Fatal("fallback failure")
				}
			})
			t.Run("shape", func(t *testing.T) {
				valid := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "id", Durable: true}}
				type shape struct {
					id      string
					result  *dispatch.DetachedResult
					failure *core.Failure
				}
				refused := []shape{
					{"", &valid, nil},
					{"", nil, &core.Failure{Code: core.InvalidRequest}},
					{"id", nil, nil},
					{"id", &valid, &core.Failure{}},
				}
				if c.shapes {
					refused = append(refused, shape{strings.Repeat("a", 65), nil, &core.Failure{Code: core.InvalidRequest}})
					for _, r := range []*dispatch.DetachedResult{
						{}, {Version: 1, Operation: core.Send}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: "other", Durable: true}}, {Version: 1, Operation: core.Send, Result: &core.Result{RequestID: "id"}}, {Version: 1, Operation: core.ContextRead}, {Version: 1, Operation: core.HistoryRead}, {Version: 1, Operation: core.Search}, {Version: 1, Operation: core.ReceiptGet, Result: valid.Result},
					} {
						refused = append(refused, shape{"id", r, nil})
					}
				}
				for _, r := range refused {
					if wire, err := c.encode(r.id, r.result, r.failure); !errors.Is(err, c.frameErr) || wire != nil {
						t.Fatalf("malformed result published: %q %+v %+v: %v", r.id, r.result, r.failure, err)
					}
				}
			})
		})
	}
}
