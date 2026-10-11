//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ProcessDirectoryMailbox handles one claimed signed/encrypted frame for the
// provisioned directory owner. Cancellation retains the input for journal replay.
func ProcessDirectoryMailbox(ctx context.Context, m *DirectoryMailbox, d *dispatch.Dispatcher, principal, binding string) error {
	if principal == "" || binding == "" {
		return store.ErrInvalid
	}
	return processDirectoryMailbox(ctx, m, d, principal, binding, nil)
}

// ProcessDirectoryMailboxAuthor handles one claimed plain frame on an author
// listener: the request runs as the author it names, else as the bound
// author. The private directory is the access gate.
func ProcessDirectoryMailboxAuthor(ctx context.Context, m *DirectoryMailbox, d *dispatch.Dispatcher, bound *dispatch.BoundAuthor) error {
	if d == nil || !d.AuthorAccess() {
		return store.ErrInvalid
	}
	return processDirectoryMailbox(ctx, m, d, "", "", bound)
}

func processDirectoryMailbox(ctx context.Context, m *DirectoryMailbox, d *dispatch.Dispatcher, principal, binding string, bound *dispatch.BoundAuthor) error {
	if m == nil || d == nil {
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
	if principal == "" {
		runLocalAuthorFrame(ctx, d, bound, body, write, func(request FrameRequest) bool {
			return request.CorrelationID == strings.TrimSuffix(name, ".json")
		})
	} else if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.CorrelationID != strings.TrimSuffix(name, ".json") || request.Stream || (request.Profile != "signed" && request.Profile != "encrypted") || request.Envelope == "" {
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
	return m.Publish(name, encoded)
}
