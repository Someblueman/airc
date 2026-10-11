//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ProcessAppendLogMailbox handles one retained frame: the request runs as
// the author it names, else as the bound author. The private log is the
// access gate. Seals advance without dispatch. Cancellation retains the
// input for journal replay.
func ProcessAppendLogMailbox(ctx context.Context, m *AppendLogMailbox, d *dispatch.Dispatcher, bound *dispatch.BoundAuthor) error {
	if m == nil || d == nil || !d.AuthorAccess() {
		return store.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := m.Claim()
	if err != nil {
		return err
	}
	if record.Next.Sealed {
		return m.Publish(nil)
	}
	body := record.Body
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
