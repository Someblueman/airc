package auth

import (
	"encoding/base64"
	"time"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	jose "github.com/go-jose/go-jose/v4"
)

const MaxEncryptedBytes = 65536

type ephemeralKey struct {
	Type  string `json:"kty"`
	Curve string `json:"crv"`
	X     string `json:"x"`
	Y     string `json:"y"`
}
type encryptedHeader struct {
	Algorithm  string       `json:"alg"`
	Encryption string       `json:"enc"`
	KeyID      string       `json:"kid"`
	Ephemeral  ephemeralKey `json:"epk"`
}

// AuthenticateEncrypted requires an authenticated compact JWS inside JWE.
// Successful decryption alone never identifies or authorizes a sender.
func (v *Verifier) AuthenticateEncrypted(token string, now time.Time) (AuthenticatedMessage, error) {
	return v.authenticateEncrypted(token, now, false)
}

func (v *Verifier) authenticateEncrypted(token string, now time.Time, retainedSend bool) (AuthenticatedMessage, error) {
	if v == nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	parts, err := compactParts(token, 5, MaxEncryptedBytes)
	if err != nil || len(parts[0]) > 2048 || parts[1] != "" {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	var h encryptedHeader
	if err := jsoninput.Decode(data, &h, 2048); err != nil || h.Algorithm != "ECDH-ES" || h.Encryption != "A256GCM" || h.Ephemeral.Type != "EC" || h.Ephemeral.Curve != "P-256" {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	for _, coordinate := range []string{h.Ephemeral.X, h.Ephemeral.Y} {
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(coordinate)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != coordinate {
			return AuthenticatedMessage{}, ErrAuthentication
		}
	}
	key, ok := v.profiles.recipients[h.KeyID]
	if !ok {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	object, err := jose.ParseEncryptedCompact(token, []jose.KeyAlgorithm{jose.ECDH_ES}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	plaintext, err := object.Decrypt(key)
	if err != nil {
		return AuthenticatedMessage{}, ErrAuthentication
	}
	defer clear(plaintext)
	return v.authenticateSigned(string(plaintext), now, retainedSend)
}
