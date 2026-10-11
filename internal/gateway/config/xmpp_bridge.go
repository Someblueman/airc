package config

import (
	"errors"
	"net"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// XMPPBridge joins fixed non-anonymous MUC rooms as one XMPP account. An
// occupant's author is chosen only from the real bare JID the room reports
// for it: a mapped AIRC account, else a guest nickname GuestPrefix + the JID
// localpart (users of the bridge account's own domain) or + the whole JID.
type XMPPBridge struct {
	Enabled          bool            `json:"enabled"`
	ID               string          `json:"id"`
	Address          string          `json:"address"`
	TLSServerName    string          `json:"tls_server_name"`
	TLSCAFile        string          `json:"tls_ca_file,omitempty"`
	Account          string          `json:"account"`
	Resource         string          `json:"resource"`
	PasswordFile     string          `json:"password_file"`
	FreshnessSeconds int64           `json:"freshness_seconds"`
	GuestPrefix      string          `json:"guest_prefix,omitempty"`
	Guests           *bool           `json:"guests,omitempty"`
	Accounts         []BridgeAccount `json:"accounts,omitempty"`
	Routes           []XMPPRoute     `json:"routes"`
}

// XMPPRoute bridges one MUC room (a bare JID) to one AIRC target.
type XMPPRoute = BridgeRoute

// GuestsAllowed reports whether unmapped occupants post as guests (default true).
func (b XMPPBridge) GuestsAllowed() bool { return b.Guests == nil || *b.Guests }

func (c Config) ValidateXMPPBridge(b XMPPBridge) error {
	if !id(b.ID) || endpoint(b.Address, true) != nil || !redisServerName(b.TLSServerName) || b.TLSCAFile != "" && !absolute(b.TLSCAFile) {
		return errors.New("XMPP bridge requires a fixed verified TLS endpoint")
	}
	host, port, _ := net.SplitHostPort(b.Address)
	p, _ := strconv.Atoi(port)
	if net.ParseIP(host).IsUnspecified() || p == 0 {
		return errors.New("XMPP bridge requires a concrete dial endpoint")
	}
	if _, _, ok := xmppwire.FullJID(b.Account + "/" + b.Resource); !ok || !absolute(b.PasswordFile) {
		return errors.New("XMPP bridge requires explicit canonical account, resource and private password file")
	}
	if b.FreshnessSeconds < 1 || b.FreshnessSeconds > 300 {
		return errors.New("XMPP bridge requires bounded freshness")
	}
	if err := c.validateBridgeRoutes("XMPP", b.Routes, xmppwire.BareJID, false); err != nil {
		return err
	}
	return validateBridgeAuthors("XMPP", b.GuestPrefix, b.Accounts, xmppwire.BareJID, b.Account)
}
