package adapter

import (
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// errResultCapacity reports a plain result too large for its carrier's body.
var errResultCapacity = errors.New("gateway result exceeds carrier capacity")

// AMQPResult is the plain JSON response body; wire delivery IDs/tags are
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

func amqpResultMessage(route config.AMQPRoute, requestID string, result *dispatch.DetachedResult, failure *core.Failure) ([]byte, error) {
	if requestID == "" || (result == nil) == (failure == nil) {
		return nil, amqpwire.ErrValue
	}
	payload, err := json.Marshal(AMQPResult{Version: 1, Protocol: "amqp/1", RequestID: requestID, RequestAddress: route.RequestAddress, ResponseAddress: route.ResponseAddress, Result: result, Error: failure})
	if err != nil {
		return nil, err
	}
	if len(payload) > amqpwire.MaxMessageBody {
		return nil, errResultCapacity
	}
	return amqpwire.EncodeDataMessage(payload)
}
