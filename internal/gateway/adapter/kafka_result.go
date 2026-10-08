package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

// kafkaOutcome is the whole first result of an intent, independent of which
// broker record carried it. The journal stores this canonical JSON, not a JWS.
type kafkaOutcome struct {
	Version   int                      `json:"version"`
	RequestID string                   `json:"request_id"`
	Operation core.Operation           `json:"operation"`
	Result    *dispatch.DetachedResult `json:"result,omitempty"`
	Error     *core.Failure            `json:"error,omitempty"`
}

func (o kafkaOutcome) validate(requestID string, operation core.Operation) error {
	if o.Version != 1 || o.RequestID != requestID || o.Operation != operation || !kafkaOperation(operation) || !kafkaRequestID(requestID) || (o.Result == nil) == (o.Error == nil) {
		return kafkawire.ErrFrame
	}
	if o.Error != nil {
		switch o.Error.Code {
		case core.AuthenticationFailed, core.Forbidden, core.UnsupportedOperation, core.InvalidRequest, core.IDConflict, core.CapacityExceeded, core.UpstreamUnavailable, core.DeliveryUncertain:
			return nil
		default:
			return kafkawire.ErrFrame
		}
	}
	r := o.Result
	if r.Version != 1 || r.Operation != operation || r.Directory != nil || r.Channels != nil || r.Agents != nil || r.Pins != nil || r.Room != nil || r.Waiting != nil || r.Poll != nil || r.Topic != nil || r.Members != nil || r.Capabilities != nil {
		return kafkawire.ErrFrame
	}
	switch operation {
	case core.Send:
		if r.Result == nil || r.Page != nil || r.Context != nil || r.Result.Version != 1 || !r.Result.Durable || r.Result.RequestID != requestID {
			return kafkawire.ErrFrame
		}
	case core.ContextRead:
		if r.Context == nil || r.Result != nil || r.Page != nil {
			return kafkawire.ErrFrame
		}
	case core.HistoryRead, core.Search:
		if r.Page == nil || r.Result != nil || r.Context != nil {
			return kafkawire.ErrFrame
		}
	}
	return nil
}

func kafkaOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

func kafkaRequestID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func encodeKafkaOutcome(o kafkaOutcome) ([]byte, error) {
	if err := o.validate(o.RequestID, o.Operation); err != nil {
		return nil, err
	}
	data, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	if err := jsoninput.Validate(data, auth.MaxSignedBytes); err != nil {
		return nil, auth.ErrResponseCapacity
	}
	return data, nil
}

func decodeKafkaOutcome(data []byte, requestID string, operation core.Operation) (kafkaOutcome, error) {
	var o kafkaOutcome
	if err := jsoninput.Validate(data, auth.MaxSignedBytes); err != nil {
		return o, kafkawire.ErrFrame
	}
	if err := json.Unmarshal(data, &o); err != nil {
		return kafkaOutcome{}, kafkawire.ErrFrame
	}
	if err := o.validate(requestID, operation); err != nil {
		return kafkaOutcome{}, err
	}
	// Only the local encoder writes this unpublished durable format. Exact
	// re-encoding rejects unknown/case-aliased fields and noncanonical encodings,
	// while preserving the intentional null acceptance fields in Send results.
	canonical, err := json.Marshal(o)
	if err != nil || !bytes.Equal(data, canonical) {
		return kafkaOutcome{}, kafkawire.ErrFrame
	}
	return o, nil
}

// KafkaResult binds a whole outcome to one original broker record and route.
// Publication success is not native AIRC acceptance.
type KafkaResult struct {
	Version         int                      `json:"version"`
	Protocol        string                   `json:"protocol"`
	RequestID       string                   `json:"request_id"`
	Namespace       string                   `json:"namespace"`
	Topic           string                   `json:"topic"`
	Partition       int32                    `json:"partition"`
	Offset          int64                    `json:"offset"`
	RecordID        string                   `json:"record_id"`
	ResultTopic     string                   `json:"result_topic"`
	ResultPartition int32                    `json:"result_partition"`
	Target          core.Target              `json:"target"`
	Result          *dispatch.DetachedResult `json:"result,omitempty"`
	Error           *core.Failure            `json:"error,omitempty"`
}

func kafkaResponseScope(realm, principal, binding string, b config.Kafka, offset int64, requestID string) auth.ResponseScope {
	recordID, _ := config.KafkaRecordID(b.Namespace, b.Route.Topic, b.Route.Partition, offset)
	sum := sha256.Sum256([]byte("kafka-response\x00" + recordID + "\x00" + requestID + "\x00" + b.Route.ResultTopic + "\x00" + strconv.FormatInt(int64(b.Route.ResultPartition), 10) + "\x00" + b.Route.Target.Kind + "\x00" + b.Route.Target.Name))
	return auth.ResponseScope{Realm: realm, Principal: principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func kafkaResultMessage(sealer *auth.ResponseSealer, scope auth.ResponseScope, b config.Kafka, offset int64, o kafkaOutcome, now time.Time) ([]byte, error) {
	recordID, err := config.KafkaRecordID(b.Namespace, b.Route.Topic, b.Route.Partition, offset)
	if err != nil || !config.KafkaTopic(b.Route.ResultTopic) || b.Route.ResultPartition < 0 || b.Route.Topic == b.Route.ResultTopic || scope.Principal != b.Route.Principal || !slices.Contains(b.Route.Operations, o.Operation) || scope != kafkaResponseScope(scope.Realm, scope.Principal, scope.Binding, b, offset, o.RequestID) {
		return nil, kafkawire.ErrFrame
	}
	if err := o.validate(o.RequestID, o.Operation); err != nil {
		return nil, err
	}
	response := KafkaResult{Version: 1, Protocol: "kafka", RequestID: o.RequestID, Namespace: b.Namespace, Topic: b.Route.Topic, Partition: b.Route.Partition, Offset: offset, RecordID: recordID, ResultTopic: b.Route.ResultTopic, ResultPartition: b.Route.ResultPartition, Target: b.Route.Target, Result: o.Result, Error: o.Error}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	token, err := sealer.Sign(scope, payload, now)
	if err != nil {
		return nil, err
	}
	if len(token) > kafkawire.MaxValue {
		return nil, auth.ErrResponseCapacity
	}
	return []byte(token), nil
}
