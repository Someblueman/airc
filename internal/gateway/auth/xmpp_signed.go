package auth

import (
	"math"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

const MaxXMPPIntentBytes = 16 << 10

// AuthenticateXMPP combines current verified MUC provenance with original signed
// AIRC intent. Delay stamps, stanza IDs and arrival time cannot refresh intent.
// The returned deadline limits execution; ReplayUntil retains the full original
// signed replay horizon so another ingress cannot re-admit a pruned send.
func (v *Verifier) AuthenticateXMPP(source VerifiedXMPPMessage, now time.Time) (AuthenticatedMessage, time.Time, error) {
	if v == nil || !source.Valid() || source.server.verifier != v || !v.OwnsIdentity(source.identity) || len(source.body) == 0 || len(source.body) > MaxXMPPIntentBytes {
		return AuthenticatedMessage{}, time.Time{}, ErrAuthentication
	}
	m, err := v.AuthenticateSigned(source.body, now)
	if err != nil {
		return AuthenticatedMessage{}, time.Time{}, err
	}
	r := m.Request
	if m.Identity.principal != source.identity.principal || r.Target == nil || *r.Target != source.target || !slices.Contains(source.operations, r.Operation) {
		return AuthenticatedMessage{}, time.Time{}, ErrForbidden
	}
	if r.Operation != core.Send && r.Operation != core.ContextRead && r.Operation != core.HistoryRead && r.Operation != core.Search {
		return AuthenticatedMessage{}, time.Time{}, ErrForbidden
	}
	freshness := int64(source.server.freshness / time.Second)
	if freshness < 1 || freshness > 300 || m.IssuedAt > math.MaxInt64-freshness || now.Unix() < m.IssuedAt {
		return AuthenticatedMessage{}, time.Time{}, ErrAuthentication
	}
	deadline := time.Unix(min(m.ExpiresAt, m.IssuedAt+freshness), 0)
	if !now.Before(deadline) {
		return AuthenticatedMessage{}, time.Time{}, ErrAuthentication
	}
	return m, deadline, nil
}
