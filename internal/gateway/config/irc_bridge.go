package config

import (
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// IRCBridge provisions one trusted server and fixed account/room grants.
type IRCBridge struct {
	Enabled          bool       `json:"enabled"`
	ID               string     `json:"id"`
	Address          string     `json:"address"`
	TLSServerName    string     `json:"tls_server_name"`
	TLSCAFile        string     `json:"tls_ca_file,omitempty"`
	Nickname         string     `json:"nickname"`
	Account          string     `json:"account"`
	PasswordFile     string     `json:"password_file"`
	FreshnessSeconds int64      `json:"freshness_seconds"`
	Routes           []IRCRoute `json:"routes"`
}

type IRCRoute struct {
	Account                        string           `json:"account"`
	Room                           string           `json:"room"`
	Principal                      string           `json:"principal"`
	Target                         core.Target      `json:"target"`
	Operations                     []core.Operation `json:"operations"`
	ResponseRecipientKeyID         string           `json:"response_recipient_key_id,omitempty"`
	ResponseRecipientPublicKeyFile string           `json:"response_recipient_public_key_file,omitempty"`
}

// IRCAccount selects literal ASCII services-account names. Names are compared
// exactly on ingress; case aliases cannot silently inherit a grant.
func IRCAccount(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// IRCRoom selects a fixed #channel subset whose folding agrees across the ASCII
// and RFC1459 case mappings. No wildcard, status prefix or multi-target list.
func IRCRoom(s string) bool {
	if len(s) < 2 || len(s) > 64 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func IRCNickname(s string) bool {
	if len(s) < 1 || len(s) > 30 || s[0] >= '0' && s[0] <= '9' || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-[]\\`^{}|_", c)) {
			return false
		}
	}
	return true
}

func (c Config) ValidateIRCBridge(b IRCBridge) error {
	if !id(b.ID) || endpoint(b.Address, true) != nil || !redisServerName(b.TLSServerName) || b.TLSCAFile != "" && !absolute(b.TLSCAFile) {
		return errors.New("IRC bridge requires a fixed verified TLS endpoint")
	}
	host, port, _ := net.SplitHostPort(b.Address)
	p, _ := strconv.Atoi(port)
	if net.ParseIP(host).IsUnspecified() || p == 0 {
		return errors.New("IRC bridge requires a concrete dial endpoint")
	}
	if !IRCNickname(b.Nickname) || !IRCAccount(b.Account) || !absolute(b.PasswordFile) {
		return errors.New("IRC bridge requires explicit nickname, services account and private password file")
	}
	if b.FreshnessSeconds < 1 || b.FreshnessSeconds > 300 || len(b.Routes) < 1 || len(b.Routes) > 128 {
		return errors.New("IRC bridge requires bounded freshness and 1-128 grants")
	}
	grants := make(map[[2]string]bool, len(b.Routes))
	for _, r := range b.Routes {
		key := [2]string{strings.ToLower(r.Account), strings.ToLower(r.Room)}
		if !IRCAccount(r.Account) || strings.EqualFold(r.Account, b.Account) || !IRCRoom(r.Room) || !id(r.Principal) || grants[key] {
			return errors.New("invalid, duplicate or bridge-account IRC grant")
		}
		grants[key] = true
		if len(r.Operations) == 0 || operations(r.Operations) != nil {
			return errors.New("IRC grant requires distinct explicit operations")
		}
		read := false
		for _, op := range r.Operations {
			read = read || op == core.ContextRead || op == core.HistoryRead || op == core.Search
		}
		if read && (c.ResponseSigningKey == nil || !id(r.ResponseRecipientKeyID) || !absolute(r.ResponseRecipientPublicKeyFile)) {
			return errors.New("IRC read grant requires an encrypted response key and response signer")
		}
		if (r.ResponseRecipientKeyID == "") != (r.ResponseRecipientPublicKeyFile == "") || r.ResponseRecipientKeyID != "" && (!id(r.ResponseRecipientKeyID) || !absolute(r.ResponseRecipientPublicKeyFile)) {
			return errors.New("IRC response key fields must be supplied together and valid")
		}
		authorized := false
		for _, p := range c.Principals {
			if p.ID != r.Principal || p.Revoked || !slices.Contains(p.Targets, r.Target) {
				continue
			}
			authorized = true
			for _, op := range r.Operations {
				if !(op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search) || !slices.Contains(p.Operations, op) {
					return errors.New("IRC grant exceeds principal authority")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("IRC history/search requires a cursor key")
				}
			}
			break
		}
		if !authorized {
			return errors.New("IRC grant requires an active principal authorized for the exact target")
		}
	}
	return nil
}
