package config

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

// IRCBridge connects to one TLS IRC server as a services account and bridges
// fixed channels. A channel member's author is chosen only from the account
// the server vouches for (IRCv3 account-tag, set by SASL or services login):
// a mapped AIRC account, else the guest nickname GuestPrefix + account.
type IRCBridge struct {
	Enabled          bool            `json:"enabled"`
	ID               string          `json:"id"`
	Address          string          `json:"address"`
	TLSServerName    string          `json:"tls_server_name"`
	TLSCAFile        string          `json:"tls_ca_file,omitempty"`
	Nickname         string          `json:"nickname"`
	Account          string          `json:"account"`
	PasswordFile     string          `json:"password_file"`
	FreshnessSeconds int64           `json:"freshness_seconds"`
	GuestPrefix      string          `json:"guest_prefix,omitempty"`
	Guests           *bool           `json:"guests,omitempty"`
	Accounts         []BridgeAccount `json:"accounts,omitempty"`
	Routes           []IRCRoute      `json:"routes"`
}

// IRCRoute bridges one #channel to one AIRC target.
type IRCRoute = BridgeRoute

// GuestsAllowed reports whether unmapped accounts post as guests (default true).
func (b IRCBridge) GuestsAllowed() bool { return b.Guests == nil || *b.Guests }

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
	if b.FreshnessSeconds < 1 || b.FreshnessSeconds > 300 {
		return errors.New("IRC bridge requires bounded freshness")
	}
	if err := c.validateBridgeRoutes("IRC", b.Routes, IRCRoom, true); err != nil {
		return err
	}
	return validateBridgeAuthors("IRC", b.GuestPrefix, b.Accounts, IRCAccount, b.Account)
}
