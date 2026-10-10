package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// AMQPResult is inside the signed response envelope; wire delivery IDs/tags are
// intentionally absent because they change on reconnect and confer no authority.
type AMQPResult struct {
	Version         int                      `json:"version"`
	Protocol        string                   `json:"protocol"`
	RequestID       string                   `json:"request_id"`
	RequestAddress  string                   `json:"request_address"`
	ResponseAddress string                   `json:"response_address"`
	Result          *dispatch.DetachedResult `json:"result,omitempty"`
	Error           *core.Failure            `json:"error,omitempty"`
}

// Arbitrary provisioned addresses do not fit ResponseScope's bounded identifier
// grammar. Domain-separated hashing binds the exact ordered pair without changing
// the established response envelope contract used by other carriers.
func amqpResponseScope(realm, principal, binding string, route config.AMQPRoute) auth.ResponseScope {
	sum := sha256.Sum256([]byte("amqp1-response\x00" + route.RequestAddress + "\x00" + route.ResponseAddress))
	return auth.ResponseScope{Realm: realm, Principal: principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func amqpResultMessage(sealer *auth.ResponseSealer, scope auth.ResponseScope, route config.AMQPRoute, requestID string, result *dispatch.DetachedResult, failure *core.Failure, now time.Time) ([]byte, error) {
	if requestID == "" || (result == nil) == (failure == nil) {
		return nil, amqpwire.ErrValue
	}
	response := AMQPResult{Version: 1, Protocol: "amqp/1", RequestID: requestID, RequestAddress: route.RequestAddress, ResponseAddress: route.ResponseAddress, Result: result, Error: failure}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	token, err := sealer.Sign(scope, payload, now)
	if err != nil {
		return nil, err
	}
	if len(token) > amqpwire.MaxMessageBody {
		return nil, auth.ErrResponseCapacity
	}
	return amqpwire.EncodeDataMessage([]byte(token))
}
