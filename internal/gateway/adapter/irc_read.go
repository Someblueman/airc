package adapter

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// BridgeResult answers a read request typed in a bridged room. It is plain
// contract JSON sent to the requester (IRC NOTICE, XMPP private message) or
// in reply to the request event (Matrix notice).
type BridgeResult struct {
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	RequestID     string                   `json:"request_id"`
	CorrelationID string                   `json:"correlation_id,omitempty"`
	Author        string                   `json:"author"`
	Room          string                   `json:"room"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

func bridgeResult(protocol, room string, e auth.BridgeEvent, result dispatch.DetachedResult, failure error) BridgeResult {
	response := BridgeResult{Version: 1, Protocol: protocol, RequestID: e.RequestID(), CorrelationID: e.CorrelationID(), Author: e.Author().Author.Nick, Room: room, Target: e.Target()}
	if failure != nil {
		status, code := httpError(failure)
		response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		response.Result = &result
	}
	return response
}

// writeBridgeResult publishes a whole result, or a capacity error when the
// carrier refuses its size before writing anything.
func writeBridgeResult(response BridgeResult, write func([]byte) error) error {
	payload, err := json.Marshal(response)
	if err == nil {
		err = write(payload)
	}
	if errors.Is(err, auth.ErrResponseCapacity) {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		payload, err = json.Marshal(response)
		if err == nil {
			err = write(payload)
		}
	}
	return err
}

func replyIRCRead(session *auth.IRCSession, proof auth.VerifiedIRCEvent, result dispatch.DetachedResult, failure error) (bool, error) {
	response := bridgeResult("irc", proof.Room(), proof.Event(), result, failure)
	err := writeBridgeResult(response, func(payload []byte) error {
		return session.WriteReadResponse(proof, payload, time.Now())
	})
	return failure == nil && err == nil, err
}
