package auth

import (
	"encoding/base64"
	"math"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	jose "github.com/go-jose/go-jose/v4"
)

const MaxSignedBytes = 32768
const ClockSkewSeconds int64 = 60

// MessageEnvelope binds validity and audience for reads as well as mutations.
// Mutation request times must explicitly match the signed envelope times.
type MessageEnvelope struct {
	Version   int          `json:"version"`
	Audience  string       `json:"audience"`
	IssuedAt  int64        `json:"issued_at"`
	ExpiresAt int64        `json:"expires_at"`
	Request   core.Request `json:"request"`
}

// Pointer validity fields distinguish an omitted value from Unix epoch zero.
type receivedEnvelope struct {
	Version   int          `json:"version"`
	Audience  string       `json:"audience"`
	IssuedAt  *int64       `json:"issued_at"`
	ExpiresAt *int64       `json:"expires_at"`
	Request   core.Request `json:"request"`
}

// AuthenticatedMessage is verified intent, not durable admission. The store
// must atomically coalesce identity/request IDs and retain signed replay markers
// through ReplayUntil. Repetition here does not prove a new operation.
type AuthenticatedMessage struct {
	Identity    Identity
	Request     core.Request
	ExpiresAt   int64
	ReplayUntil int64
}

func compactParts(token string, count, max int) ([]string, error) {
	if len(token) < 1 || len(token) > max || strings.Count(token, ".") != count-1 {
		return nil, ErrAuthentication
	}
	for _, c := range []byte(token) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return nil, ErrAuthentication
		}
	}
	return strings.Split(token, "."), nil
}

type signedHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

func (v *Verifier) AuthenticateSigned(token string, now time.Time) (AuthenticatedMessage, error) {
	if v == nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	parts, err := compactParts(token, 3, MaxSignedBytes)
	if err != nil || len(parts[0]) > 1024 {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	header, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	var h signedHeader
	if err := jsoninput.Decode(header, &h, 1024); err != nil || h.Algorithm != "EdDSA" || h.KeyID == "" {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	key, ok := v.profiles.signing[h.KeyID]
	if !ok || key.identity.principal.revoked {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	object, err := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	payload, err := object.Verify(key.public)
	if err != nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	var received receivedEnvelope
	if err := jsoninput.Decode(payload, &received, core.MaxEnvelopeBytes); err != nil || received.IssuedAt == nil || received.ExpiresAt == nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	envelope := MessageEnvelope{Version: received.Version, Audience: received.Audience, IssuedAt: *received.IssuedAt, ExpiresAt: *received.ExpiresAt, Request: received.Request}
	n := now.Unix()
	if envelope.Version != 1 || envelope.Audience != v.profiles.audience || n < 0 || envelope.IssuedAt < 0 || envelope.ExpiresAt <= envelope.IssuedAt || envelope.ExpiresAt > math.MaxInt64-ClockSkewSeconds || envelope.ExpiresAt-envelope.IssuedAt > v.profiles.maxLifetime {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	if envelope.IssuedAt > n && envelope.IssuedAt-n > ClockSkewSeconds || envelope.ExpiresAt < n && n-envelope.ExpiresAt > ClockSkewSeconds {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	r := envelope.Request
	if r.Operation == core.Send || r.Operation == core.Reply || r.Operation == core.ReactionAdd || r.Operation == core.ProfileUpdate || r.Operation == core.PresenceSet || r.Operation == core.TopicSet || r.Operation == core.RoomUpdate || r.Operation == core.PinsAdd || r.Operation == core.PinsRemove || r.Operation == core.ActivitySet || r.Operation == core.ReplySignalSet || r.Operation == core.ReplySignalCancel || r.Operation == core.PollCreate || r.Operation == core.PollVote || r.Operation == core.PollClose || r.Operation == core.MessageAction || r.Operation == core.MessageCorrect || r.Operation == core.MessageRetract {
		if r.IssuedAt == nil || r.ExpiresAt == nil || *r.IssuedAt != envelope.IssuedAt || *r.ExpiresAt != envelope.ExpiresAt {
			return AuthenticatedMessage{}, ErrAuthentication
		}
	}
	// Only now, after byte-level signature verification, normalize the content.
	if err := r.Normalize(); err != nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	if err := key.identity.Authorize(r); err != nil {
		return AuthenticatedMessage{}, err
	}
	return AuthenticatedMessage{Identity: key.identity, Request: r, ExpiresAt: envelope.ExpiresAt, ReplayUntil: envelope.ExpiresAt + ClockSkewSeconds}, nil
}
