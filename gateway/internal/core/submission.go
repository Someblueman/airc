package core

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/Someblueman/airc/gateway/internal/jsoninput"
)

// MaxPasswordBytes bounds a listener access password.
const MaxPasswordBytes = 256

// Submission is a contract request as a caller sends it on any carrier: the
// intent, who it is from, and the listener access password. Decoding splits the
// secrets off: Request never holds the account token or the password, so they
// cannot reach its JSON, its Fingerprint or the journal.
type Submission struct {
	Request Request
	// Author is nil when the request names no author (a connection may then
	// supply its identified author).
	Author     *Author
	Credential Credential
	Password   Password
}

// authorInput is the wire form of "author": {"nick": "...", "token": "..."}.
// A token makes the author a registered account; without one it is a guest.
type authorInput struct {
	Nick  string `json:"nick"`
	Token string `json:"token,omitempty"`
}

// Password is a listener access password held in memory only, like a
// Credential: it refuses JSON and redacts every format verb.
type Password struct{ value string }

func (p Password) Empty() bool { return p.value == "" }

// Matches compares in constant time against a configured password digest.
func (p Password) Matches(digest [sha256.Size]byte) bool {
	sum := sha256.Sum256([]byte(p.value))
	return p.value != "" && subtle.ConstantTimeCompare(sum[:], digest[:]) == 1
}

// NewPassword accepts a bounded, printable UTF-8 password.
func NewPassword(value string) (Password, error) {
	if value == "" || len(value) > MaxPasswordBytes || !utf8.ValidString(value) {
		return Password{}, ErrPassword
	}
	for _, r := range value {
		if r < ' ' || r == 0x7f {
			return Password{}, ErrPassword
		}
	}
	return Password{value: value}, nil
}

var ErrPassword = errors.New("invalid access password")

func (Password) Format(f fmt.State, _ rune)    { _, _ = f.Write([]byte("[redacted]")) }
func (Password) String() string                { return "[redacted]" }
func (Password) GoString() string              { return "core.Password{[redacted]}" }
func (Password) MarshalJSON() ([]byte, error)  { return nil, errors.New("password is not serializable") }
func (*Password) UnmarshalJSON(_ []byte) error { return errors.New("password is not serializable") }

// NewAuthor builds an author from a wire nickname and optional token. A
// nickname is required; a token makes the author a registered account.
func NewAuthor(nick, token string) (Author, Credential, error) {
	a := Author{Nick: nick}
	if !a.Valid() {
		return Author{}, Credential{}, ErrAuthor
	}
	if token == "" {
		return a, Credential{}, nil
	}
	c, err := NewCredential(token)
	if err != nil {
		return Author{}, Credential{}, err
	}
	a.Registered = true
	return a, c, nil
}

// DecodeSubmission parses a bounded contract request with its optional
// "author" and "password" members. Every carrier sends these same bytes, so a
// client may fall back between transports without re-encoding.
func DecodeSubmission(data []byte) (Submission, error) {
	rest, sub, err := SplitSecrets(data)
	if err != nil {
		return Submission{}, err
	}
	if sub.Request, err = Decode(rest); err != nil {
		return Submission{}, err
	}
	return sub, nil
}

// SplitSecrets removes "author" and "password" from a bounded JSON object,
// returning the remaining members for a carrier's own fixed schema (such as
// MCP tool arguments) and the author, credential and password it named.
func SplitSecrets(data []byte) ([]byte, Submission, error) {
	if err := jsoninput.Validate(data, MaxRequestBytes); err != nil {
		return nil, Submission{}, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || members == nil {
		return nil, Submission{}, errors.New("expected JSON object")
	}
	var sub Submission
	if raw, ok := members["author"]; ok {
		var in authorInput
		if err := jsoninput.Decode(raw, &in, MaxRequestBytes); err != nil {
			return nil, Submission{}, ErrAuthor
		}
		author, credential, err := NewAuthor(in.Nick, in.Token)
		if err != nil {
			return nil, Submission{}, err
		}
		sub.Author, sub.Credential = &author, credential
		delete(members, "author")
	}
	if raw, ok := members["password"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, Submission{}, ErrPassword
		}
		password, err := NewPassword(value)
		if err != nil {
			return nil, Submission{}, err
		}
		sub.Password = password
		delete(members, "password")
	}
	rest, err := json.Marshal(members)
	if err != nil {
		return nil, Submission{}, err
	}
	return rest, sub, nil
}
