package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// Anonymous executes a bounded request using the listener's shared guest
// permissions. Callers cannot select a principal, backend, or native author.
func (d *Dispatcher) Anonymous(ctx context.Context, listener string, request core.Request, now time.Time) (DetachedResult, error) {
	identity, err := d.verifier.Anonymous(listener)
	if err != nil {
		return DetachedResult{}, err
	}
	if err := request.Normalize(); err != nil {
		return DetachedResult{}, store.ErrInvalid
	}
	if err := identity.Authorize(request); err != nil {
		return DetachedResult{}, err
	}
	result := DetachedResult{Version: 1, Operation: request.Operation}
	switch request.Operation {
	case core.Send, core.ReceiptGet:
		value, err := d.execute(ctx, identity, request, 0, now)
		if err != nil {
			return DetachedResult{}, err
		}
		result.Result = &value
	case core.HistoryRead:
		page, err := d.readPage(ctx, principal(identity), request, "", now, nil)
		if err != nil {
			return DetachedResult{}, err
		}
		result.Page = &page
	default:
		return DetachedResult{}, ErrUnsupported
	}
	return result, nil
}
