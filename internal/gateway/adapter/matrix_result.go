package adapter

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// completeMatrixMessage admits one verified event. A send is acknowledged by
// durable admission only (an error retries the page); a read, or a malformed
// read, is answered with one plain notice in reply to the request event.
func completeMatrixMessage(ctx context.Context, server *auth.MatrixServer, d *dispatch.Dispatcher, proof auth.VerifiedMatrixMessage) error {
	work, cancel := context.WithTimeout(ctx, 3*time.Second)
	e := proof.Event()
	result, failure := d.Bridge(work, e, time.Now())
	cancel()
	if !e.IsRead() {
		if failure == nil && (result.Result == nil || !result.Result.Durable) {
			failure = store.ErrInvalid
		}
		return failure
	}
	response := bridgeResult("matrix", proof.Room(), e, result, failure)
	return writeBridgeResult(response, func(payload []byte) error {
		return server.WriteResponse(ctx, proof, payload, time.Now())
	})
}
