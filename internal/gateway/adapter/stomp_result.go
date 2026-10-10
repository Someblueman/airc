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
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// STOMPResult is inside the signed response envelope; wire message/acknowledgement IDs are
// intentionally absent because they change on reconnect and confer no authority.
type STOMPResult struct {
	Version             int                      `json:"version"`
	Protocol            string                   `json:"protocol"`
	RequestID           string                   `json:"request_id"`
	RequestDestination  string                   `json:"request_destination"`
	ResponseDestination string                   `json:"response_destination"`
	Result              *dispatch.DetachedResult `json:"result,omitempty"`
	Error               *core.Failure            `json:"error,omitempty"`
}

// Arbitrary provisioned addresses do not fit ResponseScope's bounded identifier
// grammar. Domain-separated hashing binds the exact ordered pair without changing
// the established response envelope contract used by other carriers.
func stompResponseScope(realm, principal, binding string, route config.STOMPRoute) auth.ResponseScope {
	sum := sha256.Sum256([]byte("stomp12-response\x00" + route.RequestDestination + "\x00" + route.ResponseDestination))
	return auth.ResponseScope{Realm: realm, Principal: principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func stompResultMessage(sealer *auth.ResponseSealer, scope auth.ResponseScope, route config.STOMPRoute, requestID string, result *dispatch.DetachedResult, failure *core.Failure, now time.Time) ([]byte, error) {
	if requestID == "" || (result == nil) == (failure == nil) {
		return nil, stompwire.ErrFrame
	}
	response := STOMPResult{Version: 1, Protocol: "stomp/1.2", RequestID: requestID, RequestDestination: route.RequestDestination, ResponseDestination: route.ResponseDestination, Result: result, Error: failure}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	token, err := sealer.Sign(scope, payload, now)
	if err != nil {
		return nil, err
	}
	if len(token) > stompwire.MaxBody {
		return nil, auth.ErrResponseCapacity
	}
	return []byte(token), nil
}
