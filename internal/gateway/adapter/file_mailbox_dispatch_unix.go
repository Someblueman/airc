//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ProcessFileMailbox handles one signed or encrypted request and publishes one
// bounded correlated result. Cancellation retains the claimed input for replay.
func ProcessFileMailbox(ctx context.Context, m *FileMailbox, d *dispatch.Dispatcher) error {
	return ProcessFileMailboxAuthor(ctx, m, d, nil)
}

// ProcessFileMailboxAuthor is ProcessFileMailbox that also takes a plain
// frame naming its author; a plain request without an author runs as the
// listener's bound author, if any.
func ProcessFileMailboxAuthor(ctx context.Context, m *FileMailbox, d *dispatch.Dispatcher, bound *dispatch.BoundAuthor) error {
	if m == nil || d == nil {
		return store.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := m.Claim()
	if err != nil {
		return err
	}
	defer clear(body)
	var request FrameRequest
	var encoded []byte
	write := func(response FrameResponse) error {
		data, err := json.Marshal(response)
		if err != nil || len(data) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		encoded = data
		return nil
	}
	if plainFrame(body) {
		runLocalAuthorFrame(ctx, d, bound, body, write, nil)
	} else if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Stream || (request.Profile != "signed" && request.Profile != "encrypted") || request.Envelope == "" {
		_ = writeCorrelatedFailure(write, request.CorrelationID, store.ErrInvalid)
	} else {
		runCorrelatedRequest(ctx, write, nil, false, d, request)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(encoded) == 0 {
		return store.ErrInvalid
	}
	defer clear(encoded)
	return m.Publish(encoded)
}
