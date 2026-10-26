//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// ProcessDirectoryMailbox handles one claimed frame: the request runs as the
// author it names, else as the bound author. The private directory is the
// access gate. Cancellation retains the input for journal replay.
func ProcessDirectoryMailbox(ctx context.Context, m *DirectoryMailbox, d *dispatch.Dispatcher, bound *dispatch.BoundAuthor) error {
	if m == nil || d == nil || !d.AuthorAccess() {
		return store.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name, body, err := m.Claim()
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
	runLocalAuthorFrame(ctx, d, bound, body, write, func(request FrameRequest) bool {
		return request.CorrelationID == strings.TrimSuffix(name, ".json")
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(encoded) == 0 {
		return store.ErrInvalid
	}
	defer clear(encoded)
	return m.Publish(name, encoded)
}
