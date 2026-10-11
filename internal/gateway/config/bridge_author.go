package config

import (
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/protocol"
)

// BridgeRoute bridges one external room to one fixed AIRC target. Any
// provider-verified member of the room may use the listed operations as the
// author the bridge maps them to; AIRC decides what that author may do.
type BridgeRoute struct {
	Room       string           `json:"room"`
	Target     core.Target      `json:"target"`
	Operations []core.Operation `json:"operations"`
}

// BridgeAccount maps one provider-verified external identity (an IRC services
// account or a bare XMPP JID) to a registered AIRC account.
// The token is read at startup and held in memory only.
type BridgeAccount struct {
	External         string `json:"external"`
	Nick             string `json:"nick"`
	AccountTokenFile string `json:"account_token_file"`
}

const (
	DefaultIRCGuestPrefix  = "irc-"
	DefaultXMPPGuestPrefix = "xmpp-"
	maxBridgeAccounts      = 1024
	maxGuestPrefix         = 16
)

// BridgeGuestPrefix is a configured guest prefix or the carrier default.
func BridgeGuestPrefix(configured, fallback string) string {
	if configured == "" {
		return fallback
	}
	return configured
}

// guestPrefix keeps derived guest nicknames inside AIRC's nickname grammar:
// a letter, then letters, digits, '-' or '_'.
func guestPrefix(prefix string) bool {
	if len(prefix) < 1 || len(prefix) > maxGuestPrefix || !protocol.ValidNick(prefix) {
		return false
	}
	c := prefix[0]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return false
	}
	for _, c := range prefix {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// validateBridgeRoutes checks the shared route shape: distinct rooms, canonical
// targets and the operations a chat bridge carries.
func (c Config) validateBridgeRoutes(kind string, routes []BridgeRoute, room func(string) bool, fold bool) error {
	if len(routes) < 1 || len(routes) > 128 {
		return errors.New(kind + " bridge requires 1-128 routes")
	}
	rooms := map[string]bool{}
	for _, r := range routes {
		key := r.Room
		if fold {
			key = strings.ToLower(key)
		}
		if !room(r.Room) || rooms[key] {
			return errors.New("invalid or duplicate " + kind + " route room")
		}
		rooms[key] = true
		target := r.Target
		if err := target.Normalize(); err != nil || target != r.Target {
			return errors.New(kind + " route target must be canonical")
		}
		if len(r.Operations) == 0 || operations(r.Operations) != nil {
			return errors.New(kind + " route requires distinct explicit operations")
		}
		for _, op := range r.Operations {
			if op != core.Send && op != core.ContextRead && op != core.HistoryRead && op != core.Search {
				return errors.New(kind + " route operations are send, context.read, history.read and search")
			}
			if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
				return errors.New(kind + " history/search requires a cursor key")
			}
		}
	}
	return nil
}

// validateBridgeAuthors checks a bridge's guest prefix and account map. The
// bridge's own provider account can never be mapped.
func validateBridgeAuthors(kind, prefix string, accounts []BridgeAccount, external func(string) bool, self string) error {
	if prefix != "" && !guestPrefix(prefix) {
		return errors.New(kind + " guest_prefix must be 1-16 letters, digits, '-' or '_' starting with a letter")
	}
	if len(accounts) > maxBridgeAccounts {
		return errors.New(kind + " account map limit exceeded")
	}
	seen := map[string]bool{}
	for _, a := range accounts {
		key := strings.ToLower(a.External)
		if !external(a.External) || seen[key] || key == strings.ToLower(self) {
			return errors.New("invalid, duplicate or bridge-account " + kind + " account mapping")
		}
		seen[key] = true
		if !protocol.ValidNick(a.Nick) || !absolute(a.AccountTokenFile) {
			return errors.New(kind + " account mapping requires an AIRC nickname and an account token file")
		}
	}
	return nil
}

// validateBridgeNamespaces keeps guest nicknames from different bridges and
// mapped accounts apart: no guest prefix may start another (ignoring case),
// and no mapped nickname may start with any guest prefix. A guest nickname
// therefore names exactly one external user of exactly one bridge.
func (c Config) validateBridgeNamespaces() error {
	var prefixes []string
	var nicks []string
	for _, b := range c.IRCBridges {
		prefixes = append(prefixes, BridgeGuestPrefix(b.GuestPrefix, DefaultIRCGuestPrefix))
		for _, a := range b.Accounts {
			nicks = append(nicks, a.Nick)
		}
	}
	for _, b := range c.XMPPBridges {
		prefixes = append(prefixes, BridgeGuestPrefix(b.GuestPrefix, DefaultXMPPGuestPrefix))
		for _, a := range b.Accounts {
			nicks = append(nicks, a.Nick)
		}
	}
	for i, p := range prefixes {
		for j, q := range prefixes {
			if i != j && strings.HasPrefix(strings.ToLower(q), strings.ToLower(p)) {
				return errors.New("bridge guest prefixes must be distinct and none may start another; set guest_prefix")
			}
		}
		for _, nick := range nicks {
			if strings.HasPrefix(strings.ToLower(nick), strings.ToLower(p)) {
				return errors.New("a mapped bridge account nickname must not start with a guest prefix")
			}
		}
	}
	return nil
}
