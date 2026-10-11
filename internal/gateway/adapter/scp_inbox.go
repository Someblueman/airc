package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/scpwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeSCPInbox acknowledges a completed upload only after its plain send
// request has been durably admitted to the native journal. The SSH login is
// the access gate; the upload posts as the author it names, else the
// inbox's bound author.
func ServeSCPInbox(ctx context.Context, stream FramedStream, d *dispatch.Dispatcher, route config.SCPInbox, author dispatch.RouteAuthor, requests chan struct{}) error {
	if ctx == nil || stream == nil || d == nil || cap(requests) < 1 {
		return store.ErrInvalid
	}
	return scpwire.Receive(ctx, stream, func(receiveCtx context.Context, payload []byte) error {
		return admitSCPInbox(receiveCtx, d, route, author, payload, requests)
	})
}

func admitSCPInbox(parent context.Context, d *dispatch.Dispatcher, route config.SCPInbox, author dispatch.RouteAuthor, payload []byte, requests chan struct{}) error {
	defer clear(payload)
	if parent == nil || len(payload) == 0 || len(payload) > MaxRequestFrameBytes {
		return auth.ErrAuthentication
	}
	sub, err := core.DecodeSubmission(payload)
	if err != nil || sub.Request.Operation != core.Send || !routeTargetMatches(sub.Request, route.Target) {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return store.ErrCapacity
	}
	result, err := d.SubmitRouteDecoded(ctx, author, dispatch.RouteSource(d.ListenerID()), sub, time.Now())
	if errors.Is(err, store.ErrClosed) {
		return err
	}
	if err != nil {
		return err
	}
	if result.Result == nil || !result.Result.Durable || result.Result.RequestID != sub.Request.RequestID {
		return store.ErrInvalid
	}
	return nil
}
