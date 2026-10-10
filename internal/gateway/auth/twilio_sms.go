package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// MaxTwilioSMSWebhookBytes bounds the form body accepted from Twilio.
const MaxTwilioSMSWebhookBytes = 16 << 10

const (
	twilioMaxFormFields = 64
	twilioMaxFormKey    = 64
	twilioMaxFormValue  = 2048
	twilioMaxMessage    = 1600
)

type twilioSMSGrant struct {
	identity Identity
	target   core.Target
}

// TwilioSMS verifies Twilio's signed form webhook and owns its Auth Token.
// Call Close only after the HTTP handler has joined all Verify calls.
type TwilioSMS struct {
	verifier   *Verifier
	accountSID string
	webhookURL string
	grants     map[[2]string]twilioSMSGrant
	token      []byte
	mu         sync.RWMutex
}

func NewTwilioSMS(v *Verifier, c config.Config, options config.TwilioSMS) (*TwilioSMS, error) {
	if v == nil || c.ValidateTwilioSMS(options) != nil {
		return nil, ErrCredential
	}
	grants := make(map[[2]string]twilioSMSGrant, len(options.Routes))
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
		grants[[2]string{route.From, route.To}] = twilioSMSGrant{identity: identity, target: target}
	}
	token, err := keyFile(options.AuthTokenFile, true)
	if err != nil {
		return nil, ErrCredential
	}
	if len(token) < 32 || len(token) > 128 {
		clear(token)
		return nil, ErrCredential
	}
	for _, b := range token {
		if b < 0x21 || b > 0x7e {
			clear(token)
			return nil, ErrCredential
		}
	}
	return &TwilioSMS{
		verifier: v, accountSID: options.AccountSID, webhookURL: options.WebhookURL,
		grants: grants, token: token,
	}, nil
}

func (s *TwilioSMS) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	clear(s.token)
	s.token = nil
	s.mu.Unlock()
}

func (s *TwilioSMS) WebhookURL() string {
	if s == nil {
		return ""
	}
	return s.webhookURL
}

// VerifiedTwilioSMS is an immutable provider-authenticated SMS submission.
// Its body remains untrusted until the dispatcher verifies the compact JWE.
type VerifiedTwilioSMS struct {
	provider   *TwilioSMS
	identity   Identity
	target     core.Target
	token      string
	messageSID string
}

func (p VerifiedTwilioSMS) Valid() bool {
	return p.provider != nil && p.identity.ID() != "" && p.token != "" && p.messageSID != ""
}
func (p VerifiedTwilioSMS) Identity() Identity  { return p.identity }
func (p VerifiedTwilioSMS) Target() core.Target { return p.target }
func (p VerifiedTwilioSMS) Token() string       { return p.token }
func (p VerifiedTwilioSMS) MessageSID() string  { return p.messageSID }

func (s *TwilioSMS) Verify(signature string, payload []byte) (VerifiedTwilioSMS, error) {
	if s == nil || len(payload) == 0 || len(payload) > MaxTwilioSMSWebhookBytes {
		return VerifiedTwilioSMS{}, ErrAuthentication
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.token) == 0 {
		return VerifiedTwilioSMS{}, ErrAuthentication
	}
	params, err := parseTwilioForm(payload)
	if err != nil || !twilioSignatureMatches(s.token, s.webhookURL, params, signature) {
		return VerifiedTwilioSMS{}, ErrAuthentication
	}
	account, accountOK := params["AccountSid"]
	sid, sidOK := params["MessageSid"]
	from, fromOK := params["From"]
	to, toOK := params["To"]
	numMedia, mediaOK := params["NumMedia"]
	body, bodyOK := params["Body"]
	if !accountOK || account != s.accountSID || !sidOK || !validTwilioMessageSID(sid) ||
		!fromOK || !toOK || numMedia != "0" || !mediaOK || !bodyOK || !validTwilioCompactJWE(body) {
		return VerifiedTwilioSMS{}, ErrAuthentication
	}
	for _, alias := range []string{"SmsSid", "SmsMessageSid"} {
		if value, ok := params[alias]; ok && value != sid {
			return VerifiedTwilioSMS{}, ErrAuthentication
		}
	}
	for _, status := range []string{"SmsStatus", "MessageStatus"} {
		if value, ok := params[status]; ok && value != "received" {
			return VerifiedTwilioSMS{}, ErrAuthentication
		}
	}
	grant, ok := s.grants[[2]string{from, to}]
	if !ok || !s.verifier.OwnsIdentity(grant.identity) || grant.identity.principal.revoked ||
		!slices.Contains(grant.identity.principal.operations, core.Send) || !slices.Contains(grant.identity.principal.targets, grant.target) {
		return VerifiedTwilioSMS{}, ErrAuthentication
	}
	return VerifiedTwilioSMS{provider: s, identity: grant.identity, target: grant.target, token: body, messageSID: sid}, nil
}

func parseTwilioForm(payload []byte) (map[string]string, error) {
	if len(payload) == 0 || len(payload) > MaxTwilioSMSWebhookBytes || !utf8.Valid(payload) {
		return nil, errors.New("invalid form")
	}
	pairs := strings.Split(string(payload), "&")
	if len(pairs) > twilioMaxFormFields {
		return nil, errors.New("too many fields")
	}
	params := make(map[string]string, len(pairs))
	aliases := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		keyText, valueText, ok := strings.Cut(pair, "=")
		if !ok || keyText == "" {
			return nil, errors.New("malformed form field")
		}
		key, err := url.QueryUnescape(keyText)
		if err != nil || len(key) == 0 || len(key) > twilioMaxFormKey {
			return nil, errors.New("invalid form key")
		}
		for _, b := range []byte(key) {
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_') {
				return nil, errors.New("invalid form key")
			}
		}
		value, err := url.QueryUnescape(valueText)
		if err != nil || len(value) > twilioMaxFormValue || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return nil, errors.New("invalid form value")
		}
		alias := strings.ToLower(key)
		if aliases[alias] {
			return nil, errors.New("duplicate form field")
		}
		aliases[alias] = true
		params[key] = value
	}
	return params, nil
}

func twilioSignatureMatches(token []byte, webhookURL string, params map[string]string, signature string) bool {
	if !validTwilioSignature(signature) {
		return false
	}
	provided, err := base64.StdEncoding.Strict().DecodeString(signature)
	if err != nil || len(provided) != sha1.Size || base64.StdEncoding.EncodeToString(provided) != signature {
		return false
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	mac := hmac.New(sha1.New, token)
	_, _ = mac.Write([]byte(webhookURL))
	for _, key := range keys {
		_, _ = mac.Write([]byte(key))
		_, _ = mac.Write([]byte(params[key]))
	}
	want := mac.Sum(nil)
	return subtle.ConstantTimeCompare(want, provided) == 1
}

func validTwilioSignature(signature string) bool {
	if len(signature) != base64.StdEncoding.EncodedLen(sha1.Size) {
		return false
	}
	for i := 0; i < len(signature); i++ {
		b := signature[i]
		if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '/' || b == '=') {
			return false
		}
	}
	return true
}

func validTwilioMessageSID(sid string) bool {
	if len(sid) != 34 || !strings.HasPrefix(sid, "SM") {
		return false
	}
	for _, b := range sid[2:] {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func validTwilioCompactJWE(value string) bool {
	if len(value) == 0 || len(value) > twilioMaxMessage || !isASCII(value) {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 5 {
		return false
	}
	for _, part := range parts {
		for _, b := range part {
			if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
				return false
			}
		}
	}
	return true
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] > 0x7f || value[i] <= 0x20 || value[i] == 0x7f {
			return false
		}
	}
	return true
}
