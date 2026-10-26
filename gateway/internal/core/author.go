package core

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/tokenfmt"
)

// Author names who a job posts as on AIRC. The gateway is not an identity
// authority: a guest author is an AIRC guest under Nick, and a registered
// author must still pass AIRC's own account authentication. Nick keeps the
// caller's spelling for display; Scope folds case exactly as AIRC does.
type Author struct {
	Nick       string `json:"nick"`
	Registered bool   `json:"registered"`
}

// ErrAuthor rejects an author nickname AIRC itself would refuse.
var ErrAuthor = errors.New("invalid author")

func (a Author) Valid() bool { return protocol.ValidNick(a.Nick) }

// Scope is the durable idempotency, receipt and cursor owner. It is global per
// nickname, never per listener or transport: a guest scope matches AIRC's own
// "guest:<nick>" retry owner, so one request ID resubmitted on any transport
// resolves to one job and one upstream message.
func (a Author) Scope() string {
	if !a.Valid() {
		return ""
	}
	if a.Registered {
		return "a:" + strings.ToLower(a.Nick)
	}
	return "g:" + strings.ToLower(a.Nick)
}

// ValidScope reports whether scope is exactly what Author.Scope produces.
func ValidScope(scope string) bool {
	if len(scope) < 3 || scope[1] != ':' || scope[0] != 'a' && scope[0] != 'g' {
		return false
	}
	a := Author{Nick: scope[2:], Registered: scope[0] == 'a'}
	return a.Scope() == scope
}

// Credential is an AIRC account token held in memory only. It is never part of
// Request, its JSON, its Fingerprint, or any journal row: marshalling fails,
// and formatting redacts it, so it cannot leak through logs or encoders.
type Credential struct{ token string }

var ErrCredential = errors.New("invalid account credential")

// NewCredential accepts only AIRC's token shape, so a malformed secret fails
// at intake rather than surfacing later as an upstream dial error.
func NewCredential(token string) (Credential, error) {
	if !tokenfmt.Valid(token) {
		return Credential{}, ErrCredential
	}
	return Credential{token: token}, nil
}

func (c Credential) Empty() bool { return c.token == "" }

// Token returns the secret for the single upstream login that needs it.
func (c Credential) Token() string { return c.token }

// Check is the only durable trace of a credential: a domain-separated digest
// used to match a resubmission after restart. AIRC tokens are 256-bit random
// values, so an unsalted digest does not enable guessing.
func (c Credential) Check(nick string) string {
	if c.token == "" {
		return ""
	}
	return TokenCheck(nick, c.token)
}

// TokenCheck is SHA-256("airc-gateway-account-v1\x00" + folded nick + token).
func TokenCheck(nick, token string) string {
	sum := sha256.Sum256([]byte("airc-gateway-account-v1\x00" + strings.ToLower(nick) + token))
	return hex.EncodeToString(sum[:])
}

// MatchesCheck compares a stored check in constant time.
func (c Credential) MatchesCheck(nick, check string) bool {
	if c.token == "" || len(check) != sha256.Size*2 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Check(nick)), []byte(check)) == 1
}

func (Credential) String() string   { return "[redacted]" }
func (Credential) GoString() string { return "core.Credential{[redacted]}" }

func (Credential) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("[redacted]")) }

func (Credential) MarshalJSON() ([]byte, error) {
	return nil, errors.New("account credential is not serializable")
}

func (*Credential) UnmarshalJSON([]byte) error {
	return errors.New("account credential is not serializable")
}

var (
	_ json.Marshaler   = Credential{}
	_ json.Unmarshaler = (*Credential)(nil)
)
