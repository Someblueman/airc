//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ProcessFileMailbox handles one frame and publishes one bounded correlated
// result: the request runs as the author it names, else as the listener's
// bound author, if any. Cancellation retains the claimed input for replay.
func ProcessFileMailbox(ctx context.Context, m *FileMailbox, d *dispatch.Dispatcher, bound *dispatch.BoundAuthor) error {
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
	var encoded []byte
	write := func(response FrameResponse) error {
		data, err := json.Marshal(response)
		if err != nil || len(data) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		encoded = data
		return nil
	}
	runLocalAuthorFrame(ctx, d, bound, body, write, nil)
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(encoded) == 0 {
		return store.ErrInvalid
	}
	defer clear(encoded)
	return m.Publish(encoded)
}
