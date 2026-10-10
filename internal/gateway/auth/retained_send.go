package auth

import (
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// RetainedSend is historical signed intent for matching a retained journal Send.
// It has no executable/replay lease. Signature, audience, original validity shape,
// future-time bound and current configured authority are still verified.
type RetainedSend struct {
	Identity Identity
	Request  core.Request
}

// VerifyRetainedSend permits expiry only for lookup of an identical durable Send.
// Callers must compare the journal fingerprint and current durable authorization;
// this result never authorizes admission, execution, reads or replay extension.
func (v *Verifier) VerifyRetainedSend(token string, now time.Time) (RetainedSend, error) {
	m, err := v.authenticateSigned(token, now, true)
	if err != nil {
		return RetainedSend{}, err
	}
	return RetainedSend{Identity: m.Identity, Request: m.Request}, nil
}

// VerifyRetainedEncryptedSend keeps the same lookup-only authority while
// requiring the signed intent to arrive inside the configured JWE profile.
func (v *Verifier) VerifyRetainedEncryptedSend(token string, now time.Time) (RetainedSend, error) {
	m, err := v.authenticateEncrypted(token, now, true)
	if err != nil {
		return RetainedSend{}, err
	}
	return RetainedSend{Identity: m.Identity, Request: m.Request}, nil
}
