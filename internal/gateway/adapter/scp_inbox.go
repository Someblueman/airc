package adapter

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/scpwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeSCPInbox acknowledges a completed upload only after its encrypted Send
// has been durably admitted to the native journal.
func ServeSCPInbox(ctx context.Context, stream FramedStream, d *dispatch.Dispatcher, route config.SCPInbox, binding string, requests chan struct{}) error {
	if ctx == nil || stream == nil || d == nil || binding == "" || cap(requests) < 1 {
		return store.ErrInvalid
	}
	return scpwire.Receive(ctx, stream, func(receiveCtx context.Context, payload []byte) error {
		return admitSCPInbox(receiveCtx, d, route, binding, payload, requests)
	})
}

func admitSCPInbox(parent context.Context, d *dispatch.Dispatcher, route config.SCPInbox, binding string, payload []byte, requests chan struct{}) error {
	defer clear(payload)
	if parent == nil || len(payload) == 0 || len(payload) > MaxRequestFrameBytes {
		return auth.ErrAuthentication
	}
	token := string(payload)
	if !validSCPEnvelope(token) {
		return auth.ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return auth.ErrAuthentication
	}

	now := time.Now()
	verified, err := d.AuthenticateDetached(token, true, now)
	if err != nil {
		retained, retainedBinding, retainedErr := d.RetainedEncryptedSend(ctx, token, route.Principal, route.Target, now)
		if errors.Is(retainedErr, store.ErrClosed) {
			return retainedErr
		}
		if retainedErr == nil && retainedBinding == binding && retained.Result != nil && retained.Result.Durable {
			return nil
		}
		return auth.ErrAuthentication
	}
	target, ok := verified.Target()
	if !ok || target != route.Target || verified.PrincipalID() != route.Principal || verified.Binding() != binding || verified.Operation() != core.Send {
		return auth.ErrAuthentication
	}
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if errors.Is(err, store.ErrClosed) {
		return err
	}
	if err != nil || result.Result == nil || !result.Result.Durable || result.Result.RequestID != verified.RequestID() {
		return auth.ErrAuthentication
	}
	return nil
}

func validSCPEnvelope(token string) bool {
	if len(token) == 0 || len(token) > MaxRequestFrameBytes || strings.Count(token, ".") != 4 {
		return false
	}
	parts := strings.Split(token, ".")
	if parts[0] == "" || parts[1] != "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
		return false
	}
	for i := range token {
		b := token[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.') {
			return false
		}
	}
	return true
}
