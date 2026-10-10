//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ProcessAppendLogMailbox handles one retained signed/encrypted log frame for
// its provisioned owner. Seals advance without dispatch. Cancellation retains the input for journal replay.
func ProcessAppendLogMailbox(ctx context.Context, m *AppendLogMailbox, d *dispatch.Dispatcher, principal, binding string) error {
	if m == nil || d == nil || principal == "" || binding == "" {
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
	if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Stream || (request.Profile != "signed" && request.Profile != "encrypted") || request.Envelope == "" {
		_ = writeCorrelatedFailure(write, request.CorrelationID, store.ErrInvalid)
	} else {
		verified, err := d.AuthenticateDetached(request.Envelope, request.Profile == "encrypted", time.Now())
		if err == nil && (verified.PrincipalID() != principal || verified.Binding() != binding) {
			err = auth.ErrAuthentication
		}
		if err != nil {
			_ = writeCorrelatedFailure(write, request.CorrelationID, err)
		} else {
			runCorrelatedVerifiedRequest(ctx, write, nil, false, d, request, verified)
		}
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
