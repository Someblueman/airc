package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

// NATSResult is inside the existing signed response envelope. Connection-local
// subscription identifiers are absent: they confer no authority or replay identity.
type NATSResult struct {
	Version         int                      `json:"version"`
	Protocol        string                   `json:"protocol"`
	RequestID       string                   `json:"request_id"`
	RequestSubject  string                   `json:"request_subject"`
	ResponseSubject string                   `json:"response_subject"`
	Target          core.Target              `json:"target"`
	Result          *dispatch.DetachedResult `json:"result,omitempty"`
	Error           *core.Failure            `json:"error,omitempty"`
}

// Ordered literal subjects and target are hashed under a protocol-specific
// domain to fit the established ResponseScope route identifier grammar.
func natsResponseScope(realm, principal, binding string, route config.NATSRoute) auth.ResponseScope {
	sum := sha256.Sum256([]byte("nats-core-response\x00" + route.RequestSubject + "\x00" + route.ResponseSubject + "\x00" + route.Target.Kind + "\x00" + route.Target.Name))
	return auth.ResponseScope{Realm: realm, Principal: principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func natsResultMessage(sealer *auth.ResponseSealer, scope auth.ResponseScope, route config.NATSRoute, requestID string, result *dispatch.DetachedResult, failure *core.Failure, now time.Time) ([]byte, error) {
	if requestID == "" || len(requestID) > 64 || (result == nil) == (failure == nil) || !natswire.Subject(route.RequestSubject) || !natswire.Subject(route.ResponseSubject) || route.RequestSubject == route.ResponseSubject || scope.Principal != route.Principal || scope != natsResponseScope(scope.Realm, scope.Principal, scope.Binding, route) {
		return nil, natswire.ErrFrame
	}
	if result != nil {
		if result.Version != 1 || !natsRequestOperation(result.Operation) {
			return nil, natswire.ErrFrame
		}
		switch result.Operation {
		case core.Send:
			if result.Result == nil || !result.Result.Durable || result.Result.RequestID != requestID {
				return nil, natswire.ErrFrame
			}
		case core.ContextRead:
			if result.Context == nil {
				return nil, natswire.ErrFrame
			}
		case core.HistoryRead, core.Search:
			if result.Page == nil {
				return nil, natswire.ErrFrame
			}
		}
	}
	response := NATSResult{Version: 1, Protocol: "nats/core", RequestID: requestID, RequestSubject: route.RequestSubject, ResponseSubject: route.ResponseSubject, Target: route.Target, Result: result, Error: failure}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	token, err := sealer.Sign(scope, payload, now)
	if err != nil {
		return nil, err
	}
	if len(token) > natswire.MaxPayload {
		return nil, auth.ErrResponseCapacity
	}
	return []byte(token), nil
}
