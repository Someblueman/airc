package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// MatrixResult is published only as a whole signed and encrypted response.
// Clients correlate its protected fields, not the containing Matrix event ID.
type MatrixResult struct {
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	RequestID     string                   `json:"request_id"`
	SourceEventID string                   `json:"source_event_id"`
	CorrelationID string                   `json:"correlation_id,omitempty"`
	Author        string                   `json:"author"`
	Room          string                   `json:"room"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

func completeMatrixMessage(ctx context.Context, server *auth.MatrixServer, d *dispatch.Dispatcher, proof auth.VerifiedMatrixMessage) error {
	work, cancel := context.WithTimeout(ctx, 3*time.Second)
	var result dispatch.DetachedResult
	var failure error
	if proof.IsRead() {
		result, failure = d.MatrixRead(work, proof, time.Now())
	} else {
		var delivery core.Result
		delivery, failure = d.Matrix(work, proof, time.Now())
		if failure == nil && !delivery.Durable {
			failure = store.ErrInvalid
		}
		result = dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &delivery}
	}
	cancel()
	response := MatrixResult{Version: 1, Protocol: "matrix", RequestID: proof.RequestID(), SourceEventID: proof.EventID(), CorrelationID: proof.ReadCorrelationID(), Author: proof.Author(), Room: proof.Room(), Target: proof.Target()}
	if failure == nil {
		response.Result = &result
	} else {
		status, code := httpError(failure)
		response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	}
	payload, err := json.Marshal(response)
	if err == nil {
		err = server.WriteResponse(ctx, proof, payload, time.Now())
	}
	if errors.Is(err, auth.ErrResponseCapacity) {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		payload, err = json.Marshal(response)
		if err == nil {
			err = server.WriteResponse(ctx, proof, payload, time.Now())
		}
	}
	if err != nil {
		return err
	}
	if !proof.IsRead() {
		// Sending an error reply cannot acknowledge a failed durable admission.
		return failure
	}
	return nil // A complete protected read result or failure was published.
}
