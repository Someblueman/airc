package adapter

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// SQSResult is inside the standard signed-and-encrypted response envelope.
// Queue delivery, publication and deletion never establish native acceptance.
type SQSResult struct {
	Version         int                      `json:"version"`
	Protocol        string                   `json:"protocol"`
	RequestID       string                   `json:"request_id"`
	RequestQueueURL string                   `json:"request_queue_url"`
	ResultQueueURL  string                   `json:"result_queue_url"`
	Target          core.Target              `json:"target"`
	Result          *dispatch.DetachedResult `json:"result,omitempty"`
	Error           *core.Failure            `json:"error,omitempty"`
}

// SQSResponseScope binds the fixed queue pair and target under its own domain.
// RequestID is authenticated in SQSResult, independent of SQS message metadata.
func SQSResponseScope(realm, principal, binding string, b config.SQS) auth.ResponseScope {
	sum := sha256.Sum256([]byte("sqs-response\x00" + b.RequestQueueURL + "\x00" + b.Route.ResultQueueURL + "\x00" + b.Route.Target.Kind + "\x00" + b.Route.Target.Name))
	return auth.ResponseScope{Realm: realm, Principal: principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func sqsResultMessage(sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, realm string, b config.SQS, a sqsAdmission, now time.Time) (string, error) {
	if a.refused || a.requestID == "" || a.binding == "" || (a.result == nil) == (a.failure == nil) {
		return "", store.ErrInvalid
	}
	if r := a.result; r != nil {
		if r.Version != 1 || !sqsOperation(r.Operation) {
			return "", store.ErrInvalid
		}
		switch r.Operation {
		case core.Send:
			if r.Result == nil || !r.Result.Durable || r.Result.RequestID != a.requestID {
				return "", store.ErrInvalid
			}
		case core.ContextRead:
			if r.Context == nil {
				return "", store.ErrInvalid
			}
		case core.HistoryRead, core.Search:
			if r.Page == nil {
				return "", store.ErrInvalid
			}
		}
	}
	payload, err := json.Marshal(SQSResult{Version: 1, Protocol: "aws/sqs", RequestID: a.requestID, RequestQueueURL: b.RequestQueueURL, ResultQueueURL: b.Route.ResultQueueURL, Target: b.Route.Target, Result: a.result, Error: a.failure})
	if err != nil {
		return "", err
	}
	return sealer.Seal(SQSResponseScope(realm, b.Route.Principal, a.binding, b), b.Route.RecipientKeyID, recipient, payload, now)
}
