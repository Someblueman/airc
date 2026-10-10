package adapter

import (
	"encoding/json"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// STOMPResult is the plain JSON response body; wire message/acknowledgement IDs
// are intentionally absent because they change on reconnect and confer no authority.
type STOMPResult struct {
	Version             int                      `json:"version"`
	Protocol            string                   `json:"protocol"`
	RequestID           string                   `json:"request_id"`
	RequestDestination  string                   `json:"request_destination"`
	ResponseDestination string                   `json:"response_destination"`
	Result              *dispatch.DetachedResult `json:"result,omitempty"`
	Error               *core.Failure            `json:"error,omitempty"`
}

func stompResultMessage(route config.STOMPRoute, requestID string, result *dispatch.DetachedResult, failure *core.Failure) ([]byte, error) {
	if requestID == "" || (result == nil) == (failure == nil) {
		return nil, stompwire.ErrFrame
	}
	payload, err := json.Marshal(STOMPResult{Version: 1, Protocol: "stomp/1.2", RequestID: requestID, RequestDestination: route.RequestDestination, ResponseDestination: route.ResponseDestination, Result: result, Error: failure})
	if err != nil {
		return nil, err
	}
	if len(payload) > stompwire.MaxBody {
		return nil, errResultCapacity
	}
	return payload, nil
}
