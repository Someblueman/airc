package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	jose "github.com/go-jose/go-jose/v4"
)

const (
	MaxResponsePayloadBytes = 32 << 10
	MaxResponseTokenBytes   = 64 << 10
	ResponseLifetime        = 120 * time.Second
)

var ErrResponseCapacity = errors.New("response_capacity")

type ResponseScope struct {
	Realm     string `json:"realm"`
	Principal string `json:"principal"`
	Binding   string `json:"binding"`
	Route     string `json:"route"`
}

type ResponseSealer struct {
	signingID  string
	signingKey ed25519.PrivateKey
}

type responseJWSHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type responseJWEHeader struct {
	Algorithm  string        `json:"alg"`
	Encryption string        `json:"enc"`
	KeyID      string        `json:"kid"`
	Type       string        `json:"typ"`
	Ephemeral  *ephemeralKey `json:"epk"`
}

type responseEnvelope struct {
	Version   int             `json:"version"`
	Purpose   string          `json:"purpose"`
	Scope     ResponseScope   `json:"scope"`
	IssuedAt  int64           `json:"issued_at"`
	ExpiresAt int64           `json:"expires_at"`
	Payload   json.RawMessage `json:"payload"`
}

func NewResponseSealer(signingID string, signingKey ed25519.PrivateKey) (*ResponseSealer, error) {
	if !validResponseID(signingID) || len(signingKey) != ed25519.PrivateKeySize || !validSigningKey(signingKey) {
		return nil, ErrCredential
	}
	return &ResponseSealer{signingID: signingID, signingKey: append(ed25519.PrivateKey(nil), signingKey...)}, nil
}

func (s *ResponseSealer) Seal(scope ResponseScope, recipientID string, recipient *ecdsa.PublicKey, payload []byte, now time.Time) (string, error) {
	if s == nil || !validResponseID(s.signingID) || len(s.signingKey) != ed25519.PrivateKeySize {
		return "", ErrCredential
	}
	if !validResponseScope(scope) || !validResponseID(recipientID) || !validP256Public(recipient) || now.Unix() < 0 || now.Unix() > math.MaxInt64-int64(ResponseLifetime/time.Second) {
		return "", ErrAuthentication
	}
	signedToken, err := s.Sign(scope, payload, now)
	if err != nil {
		return "", err
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: recipient, KeyID: recipientID}, (&jose.EncrypterOptions{}).WithType(jose.ContentType("airc-gateway-response+jwe")))
	if err != nil {
		return "", ErrCredential
	}
	sealed, err := encrypter.Encrypt([]byte(signedToken))
	if err != nil {
		return "", ErrAuthentication
	}
	token, err := sealed.CompactSerialize()
	if err != nil {
		return "", ErrAuthentication
	}
	if len(token) > MaxResponseTokenBytes {
		return "", ErrResponseCapacity
	}
	return token, nil
}

func OpenResponse(token, signingID string, signingKey ed25519.PublicKey, recipientID string, recipient *ecdsa.PrivateKey, expected ResponseScope, now time.Time) ([]byte, error) {
	if !validResponseID(signingID) || len(signingKey) != ed25519.PublicKeySize || !validResponseID(recipientID) || !validP256Private(recipient) || !validResponseScope(expected) {
		return nil, ErrAuthentication
	}
	parts, err := compactParts(token, 5, MaxResponseTokenBytes)
	if err != nil || parts[1] != "" {
		return nil, ErrAuthentication
	}
	var outer responseJWEHeader
	if err := decodeResponseHeader(parts[0], &outer); err != nil || outer.Algorithm != "ECDH-ES" || outer.Encryption != "A256GCM" || outer.KeyID != recipientID || outer.Type != "airc-gateway-response+jwe" || !validEphemeral(outer.Ephemeral) {
		return nil, ErrAuthentication
	}
	object, err := jose.ParseEncryptedCompact(token, []jose.KeyAlgorithm{jose.ECDH_ES}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return nil, ErrAuthentication
	}
	inner, err := object.Decrypt(recipient)
	if err != nil {
		return nil, ErrAuthentication
	}
	defer clear(inner)
	return OpenSignedResponse(string(inner), signingID, signingKey, expected, now)
}

func decodeResponseHeader(part string, dst any) error {
	data, err := decodePart(part)
	if err != nil || len(data) > 2048 {
		return ErrAuthentication
	}
	return jsoninput.Decode(data, dst, 2048)
}

func decodeResponseEnvelope(data []byte) (responseEnvelope, error) {
	var received struct {
		Version   int                        `json:"version"`
		Purpose   string                     `json:"purpose"`
		Scope     ResponseScope              `json:"scope"`
		IssuedAt  *int64                     `json:"issued_at"`
		ExpiresAt *int64                     `json:"expires_at"`
		Payload   map[string]json.RawMessage `json:"payload"`
	}
	if err := jsoninput.Decode(data, &received, MaxResponsePayloadBytes+4096); err != nil || received.IssuedAt == nil || received.ExpiresAt == nil || !validResponseScope(received.Scope) {
		return responseEnvelope{}, ErrAuthentication
	}
	// The fixed-schema decoder treats byte slices as arrays. Preserve the raw
	// object separately after checking the envelope's names and null fields.
	var envelope responseEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return responseEnvelope{}, ErrAuthentication
	}
	if err := validateResponsePayload(envelope.Payload); err != nil {
		return responseEnvelope{}, err
	}
	return envelope, nil
}

func decodePart(part string) ([]byte, error) {
	return base64.RawURLEncoding.Strict().DecodeString(part)
}

func validateResponsePayload(payload []byte) error {
	if len(payload) > MaxResponsePayloadBytes {
		return ErrResponseCapacity
	}
	trimmed := bytes.TrimSpace(payload)
	if len(payload) == 0 || len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return ErrAuthentication
	}
	if err := jsoninput.Validate(payload, MaxResponsePayloadBytes); err != nil {
		return ErrAuthentication
	}
	return nil
}

func validSigningKey(key ed25519.PrivateKey) bool {
	if len(key) != ed25519.PrivateKeySize {
		return false
	}
	derived := ed25519.NewKeyFromSeed(key.Seed())
	defer clear(derived)
	return subtle.ConstantTimeCompare(derived[ed25519.SeedSize:], key[ed25519.SeedSize:]) == 1
}

func validResponseScope(scope ResponseScope) bool {
	return validResponseID(scope.Realm) && validResponseID(scope.Principal) && validResponseID(scope.Binding) && validResponseID(scope.Route)
}

func validResponseID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validP256Public(key *ecdsa.PublicKey) bool {
	if key == nil || key.Curve != elliptic.P256() {
		return false
	}
	_, err := key.ECDH()
	return err == nil
}

func validP256Private(key *ecdsa.PrivateKey) bool {
	if key == nil || !validP256Public(&key.PublicKey) {
		return false
	}
	private, err := key.ECDH()
	if err != nil {
		return false
	}
	public, err := key.PublicKey.ECDH()
	return err == nil && subtle.ConstantTimeCompare(private.PublicKey().Bytes(), public.Bytes()) == 1
}

func validEphemeral(key *ephemeralKey) bool {
	if key == nil || key.Type != "EC" || key.Curve != "P-256" {
		return false
	}
	x, errX := base64.RawURLEncoding.Strict().DecodeString(key.X)
	y, errY := base64.RawURLEncoding.Strict().DecodeString(key.Y)
	return errX == nil && errY == nil && len(x) == 32 && len(y) == 32 && base64.RawURLEncoding.EncodeToString(x) == key.X && base64.RawURLEncoding.EncodeToString(y) == key.Y
}
