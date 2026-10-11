package adapter

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// runLocalAuthorFrame runs one plain frame from a one-shot local carrier
// (mailboxes, queues, shared memory, mapped files) as the author it names,
// else as the listener's bound author. The carrier's own owner-only boundary
// is the access gate; AIRC sees listener:<id> as the source. One response
// frame (a result or an error) is written; streams are refused because a
// one-shot carrier has nowhere to keep them. accept, when set, adds the
// carrier's own frame checks (such as a file name matching the correlation ID).
func runLocalAuthorFrame(ctx context.Context, d *dispatch.Dispatcher, bound *dispatch.BoundAuthor, body []byte, write func(FrameResponse) error, accept func(FrameRequest) bool) {
	request, plain, err := decodeFrame(body)
	if err != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Stream || plain == nil || request.Profile != "" && request.Profile != "plain" || accept != nil && !accept(request) {
		_ = writeCorrelatedFailure(write, request.CorrelationID, store.ErrInvalid)
		return
	}
	if d == nil || !d.AuthorAccess() {
		_ = writeCorrelatedFailure(write, request.CorrelationID, dispatch.ErrUnsupported)
		return
	}
	runCorrelatedPlainRequest(ctx, write, nil, false, d, request, bound.Ingress(""), plain)
}
