package auth

import (
	"crypto/ed25519"
	"encoding/json"
	"math"
	"time"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	jose "github.com/go-jose/go-jose/v4"
)

// Sign uses the same response envelope as Seal, without recipient encryption.
// Carriers using this profile must provide transport confidentiality separately.
func (s *ResponseSealer) Sign(scope ResponseScope, payload []byte, now time.Time) (string, error) {
	if s == nil || !validResponseID(s.signingID) || len(s.signingKey) != ed25519.PrivateKeySize {
		return "", ErrCredential
	}
	if !validResponseScope(scope) || now.Unix() < 0 || now.Unix() > math.MaxInt64-int64(ResponseLifetime/time.Second) {
		return "", ErrAuthentication
	}
	if err := validateResponsePayload(payload); err != nil {
		return "", err
	}
	envelope := responseEnvelope{Version: 1, Purpose: "airc-gateway-response", Scope: scope, IssuedAt: now.Unix(), ExpiresAt: now.Unix() + int64(ResponseLifetime/time.Second), Payload: json.RawMessage(payload)}
	plain, err := json.Marshal(envelope)
	if err != nil {
		return "", ErrAuthentication
	}
	// Receiver envelope limits include escaping, nesting and scope overhead.
	if err := jsoninput.Validate(plain, MaxResponsePayloadBytes+4096); err != nil {
		return "", ErrResponseCapacity
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: s.signingKey}, (&jose.SignerOptions{}).WithHeader("kid", s.signingID).WithType(jose.ContentType("airc-gateway-response+jws")))
	if err != nil {
		return "", ErrCredential
	}
	signed, err := signer.Sign(plain)
	if err != nil {
		return "", ErrAuthentication
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		return "", ErrAuthentication
	}
	if len(token) > MaxResponseTokenBytes {
		return "", ErrResponseCapacity
	}
	return token, nil
}

// OpenSignedResponse verifies the protected key/type, exact scope, bounded
// payload and signed validity. It shares its checks with encrypted responses.
func OpenSignedResponse(token, signingID string, signingKey ed25519.PublicKey, expected ResponseScope, now time.Time) ([]byte, error) {
	if !validResponseID(signingID) || len(signingKey) != ed25519.PublicKeySize || !validResponseScope(expected) {
		return nil, ErrAuthentication
	}
	parts, err := compactParts(token, 3, MaxResponseTokenBytes)
	if err != nil {
		return nil, ErrAuthentication
	}
	var header responseJWSHeader
	if err := decodeResponseHeader(parts[0], &header); err != nil || header.Algorithm != "EdDSA" || header.KeyID != signingID || header.Type != "airc-gateway-response+jws" {
		return nil, ErrAuthentication
	}
	signed, err := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return nil, ErrAuthentication
	}
	plain, err := signed.Verify(signingKey)
	if err != nil {
		return nil, ErrAuthentication
	}
	defer clear(plain)
	envelope, err := decodeResponseEnvelope(plain)
	n := now.Unix()
	if err != nil || n < 0 || envelope.Version != 1 || envelope.Purpose != "airc-gateway-response" || envelope.Scope != expected || envelope.IssuedAt < 0 || envelope.ExpiresAt <= envelope.IssuedAt || envelope.ExpiresAt-envelope.IssuedAt > int64(ResponseLifetime/time.Second) || (envelope.IssuedAt > n && envelope.IssuedAt-n > ClockSkewSeconds) || (envelope.ExpiresAt < n && n-envelope.ExpiresAt > ClockSkewSeconds) {
		return nil, ErrAuthentication
	}
	return append([]byte(nil), envelope.Payload...), nil
}
