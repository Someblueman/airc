package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

// XMPP authenticates the signed body against verified MUC author/room mapping,
// then uses ordinary durable admission or whole bounded reads. The returned
// request ID is signed application correlation, never a provider stanza ID.
// An empty ID means no authenticated response scope was established.
func (d *Dispatcher) XMPP(ctx context.Context, source auth.VerifiedXMPPMessage, now time.Time) (DetachedResult, string, error) {
	if d == nil || d.verifier == nil || d.journal == nil {
		return DetachedResult{}, "", auth.ErrAuthentication
	}
	message, deadline, err := d.verifier.AuthenticateXMPP(source, now)
	if err != nil {
		return DetachedResult{}, "", err
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	verified := VerifiedMessage{owner: d, message: message, verifiedAt: now.Unix()}
	result, err := d.DetachedVerified(ctx, verified, now)
	return result, message.Request.RequestID, err
}
