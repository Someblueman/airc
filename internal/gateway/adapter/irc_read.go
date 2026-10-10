package adapter

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// IRCReadResult is sent only inside a signed and encrypted response token.
// Chunk tags and nickname routing do not authenticate or reveal its payload.
type IRCReadResult struct {
	CorrelationID string                   `json:"correlation_id"`
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	RequestID     string                   `json:"request_id"`
	Account       string                   `json:"account"`
	Room          string                   `json:"room"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

func replyIRCRead(session *auth.IRCSession, proof auth.VerifiedIRCEvent, result dispatch.DetachedResult, failure error) (bool, error) {
	response := IRCReadResult{Version: 1, Protocol: "irc/read", CorrelationID: proof.ReadCorrelationID(), RequestID: proof.RequestID(), Account: proof.Account(), Room: proof.Room(), Target: proof.Target()}
	if failure != nil {
		status, code := httpError(failure)
		response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		response.Result = &result
	}
	payload, err := json.Marshal(response)
	if err == nil {
		err = session.WriteReadResponse(proof, payload, time.Now())
	}
	completed := failure == nil
	if errors.Is(err, auth.ErrResponseCapacity) {
		// The sealer refuses whole oversized results before writing any chunks.
		completed = false
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		payload, err = json.Marshal(response)
		if err == nil {
			err = session.WriteReadResponse(proof, payload, time.Now())
		}
	}
	return completed && err == nil, err
}
