package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

// MaxPostmarkEmailBytes bounds inbound JSON before any parsing.
const MaxPostmarkEmailBytes = 256 << 10

const maxPostmarkTextBodyBytes = 16 << 10

type postmarkEmailGrant struct {
	identity Identity
	target   core.Target
}

// PostmarkEmail authenticates the configured Basic Auth credential and maps
// provider address pairs to immutable verifier-owned send grants.
type PostmarkEmail struct {
	verifier           *Verifier
	credentialHash     [sha256.Size]byte
	authorizationBytes int
	webhookURL         string
	grants             map[[2]string]postmarkEmailGrant
	mu                 sync.RWMutex
}

func NewPostmarkEmail(v *Verifier, c config.Config, options config.PostmarkEmail) (*PostmarkEmail, error) {
	if v == nil || c.ValidatePostmarkEmail(options) != nil {
		return nil, ErrCredential
	}
	grants := make(map[[2]string]postmarkEmailGrant, len(options.Routes))
	for _, route := range options.Routes {
		target := route.Target
		if target.Normalize() != nil {
			return nil, ErrCredential
		}
		var identity Identity
		for i := range v.principals {
			candidate := Identity{&v.principals[i]}
			if candidate.ID() != route.Principal || !v.OwnsIdentity(candidate) || !slices.Contains(candidate.principal.operations, core.Send) {
				continue
			}
			for _, principalTarget := range candidate.principal.targets {
				if principalTarget.Normalize() == nil && principalTarget == target {
					identity = candidate
					break
				}
			}
			if identity.ID() != "" {
				break
			}
		}
		if identity.ID() == "" {
			return nil, ErrCredential
		}
		grants[[2]string{route.From, route.To}] = postmarkEmailGrant{identity: identity, target: target}
	}
	password, err := keyFile(options.PasswordFile, true)
	if err != nil || len(password) < 32 || len(password) > 128 {
		clear(password)
		return nil, ErrCredential
	}
	for _, b := range password {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			clear(password)
			return nil, ErrCredential
		}
	}
	defer clear(password)
	credential := make([]byte, 0, len(options.Username)+1+len(password))
	credential = append(credential, options.Username...)
	credential = append(credential, ':')
	credential = append(credential, password...)
	defer clear(credential)
	const prefix = "Basic "
	header := make([]byte, len(prefix)+base64.StdEncoding.EncodedLen(len(credential)))
	copy(header, prefix)
	base64.StdEncoding.Encode(header[len(prefix):], credential)
	defer clear(header)
	return &PostmarkEmail{
		verifier: v, credentialHash: sha256.Sum256(header), authorizationBytes: len(header),
		webhookURL: options.WebhookURL, grants: grants,
	}, nil
}

func (p *PostmarkEmail) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	clear(p.credentialHash[:])
	p.authorizationBytes = 0
	p.mu.Unlock()
}

func (p *PostmarkEmail) WebhookURL() string {
	if p == nil {
		return ""
	}
	return p.webhookURL
}

type VerifiedPostmarkEmail struct {
	provider  *PostmarkEmail
	identity  Identity
	target    core.Target
	token     string
	messageID string
}

func (m VerifiedPostmarkEmail) Valid() bool {
	return m.provider != nil && m.identity.ID() != "" && m.token != "" && m.messageID != ""
}
func (m VerifiedPostmarkEmail) Identity() Identity  { return m.identity }
func (m VerifiedPostmarkEmail) Target() core.Target { return m.target }
func (m VerifiedPostmarkEmail) Token() string       { return m.token }
func (m VerifiedPostmarkEmail) MessageID() string   { return m.messageID }

func (p *PostmarkEmail) Verify(authorization string, payload []byte) (VerifiedPostmarkEmail, error) {
	if p == nil || len(payload) == 0 || len(payload) > MaxPostmarkEmailBytes {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.authorizationBytes == 0 || len(authorization) != p.authorizationBytes {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	provided := sha256.Sum256([]byte(authorization))
	if subtle.ConstantTimeCompare(p.credentialHash[:], provided[:]) != 1 || jsoninput.ValidateDepth(payload, MaxPostmarkEmailBytes, 16) != nil {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	selected := []string{"MessageStream", "From", "FromFull", "OriginalRecipient", "ToFull", "TextBody", "Attachments", "MessageID", "RecordType", "Cc", "Bcc", "CcFull", "BccFull"}
	if !postmarkExactFields(object, selected) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	messageStream, ok := postmarkString(object, "MessageStream")
	if !ok || messageStream != "inbound" {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	from, ok := postmarkString(object, "From")
	if !ok || !config.ValidPostmarkAddress(from) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	originalRecipient, ok := postmarkString(object, "OriginalRecipient")
	if !ok || !config.ValidPostmarkAddress(originalRecipient) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	messageID, ok := postmarkString(object, "MessageID")
	if !ok || !validPostmarkUUID(messageID) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	if _, exists := object["RecordType"]; exists {
		value, ok := postmarkString(object, "RecordType")
		if !ok || value != "Inbound" {
			return VerifiedPostmarkEmail{}, ErrAuthentication
		}
	}
	if !postmarkFromFullMatches(object["FromFull"], from) || !postmarkToFullMatches(object["ToFull"], originalRecipient) || !postmarkNoExtraRecipients(object) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	attachments, exists := object["Attachments"]
	if !exists || !postmarkEmptyArray(attachments) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	body, ok := postmarkString(object, "TextBody")
	if !ok || len(body) > maxPostmarkTextBodyBytes {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	token := strings.Trim(body, " \t\r\n")
	if !validPostmarkCompactJWE(token) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	grant, ok := p.grants[[2]string{from, originalRecipient}]
	if !ok || !p.verifier.OwnsIdentity(grant.identity) || grant.identity.principal.revoked ||
		!slices.Contains(grant.identity.principal.operations, core.Send) || !slices.Contains(grant.identity.principal.targets, grant.target) {
		return VerifiedPostmarkEmail{}, ErrAuthentication
	}
	return VerifiedPostmarkEmail{provider: p, identity: grant.identity, target: grant.target, token: token, messageID: messageID}, nil
}

func postmarkExactFields(object map[string]json.RawMessage, names []string) bool {
	for key := range object {
		for _, name := range names {
			if strings.EqualFold(key, name) && key != name {
				return false
			}
		}
	}
	return true
}

func postmarkString(object map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := object[key]
	if !ok || len(raw) == 0 {
		return "", false
	}
	if raw[0] != '"' {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func postmarkFromFullMatches(raw json.RawMessage, from string) bool {
	var object map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &object) != nil || object == nil || !postmarkExactFields(object, []string{"Email"}) {
		return false
	}
	email, ok := postmarkString(object, "Email")
	return ok && email == from
}

func postmarkToFullMatches(raw json.RawMessage, to string) bool {
	var recipients []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &recipients) != nil || len(recipients) != 1 {
		return false
	}
	var recipient map[string]json.RawMessage
	if json.Unmarshal(recipients[0], &recipient) != nil || recipient == nil || !postmarkExactFields(recipient, []string{"Email"}) {
		return false
	}
	email, ok := postmarkString(recipient, "Email")
	return ok && email == to
}

func postmarkNoExtraRecipients(object map[string]json.RawMessage) bool {
	for _, key := range []string{"Cc", "Bcc"} {
		if value, ok := object[key]; ok {
			text, valid := postmarkString(object, key)
			if !valid || text != "" || len(value) == 0 {
				return false
			}
		}
	}
	for _, key := range []string{"CcFull", "BccFull"} {
		if value, ok := object[key]; ok && !postmarkEmptyArray(value) {
			return false
		}
	}
	return true
}

func postmarkEmptyArray(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && len(values) == 0
}

func validPostmarkUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func validPostmarkCompactJWE(value string) bool {
	if len(value) == 0 || len(value) > maxPostmarkTextBodyBytes {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 5 {
		return false
	}
	for _, part := range parts {
		for i := 0; i < len(part); i++ {
			b := part[i]
			if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
				return false
			}
		}
	}
	return true
}
