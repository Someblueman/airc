package config

import (
	"errors"
	"net"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

// MatrixBridge reads fixed joined rooms through one homeserver account. A
// sender's author is chosen only from the user ID the authenticated
// homeserver reports: a mapped AIRC account, else a guest nickname
// GuestPrefix + the localpart (users of the bridge account's own homeserver)
// or + the whole user ID.
type MatrixBridge struct {
	Enabled          bool            `json:"enabled"`
	ID               string          `json:"id"`
	Address          string          `json:"address"`
	TLSServerName    string          `json:"tls_server_name"`
	TLSCAFile        string          `json:"tls_ca_file,omitempty"`
	Account          string          `json:"account"`
	AccessTokenFile  string          `json:"access_token_file"`
	FreshnessSeconds int64           `json:"freshness_seconds"`
	GuestPrefix      string          `json:"guest_prefix,omitempty"`
	Guests           *bool           `json:"guests,omitempty"`
	Accounts         []BridgeAccount `json:"accounts,omitempty"`
	Routes           []MatrixRoute   `json:"routes"`
}

// MatrixRoute bridges one room ID to one AIRC target.
type MatrixRoute = BridgeRoute

// GuestsAllowed reports whether unmapped senders post as guests (default true).
func (b MatrixBridge) GuestsAllowed() bool { return b.Guests == nil || *b.Guests }

func (c Config) ValidateMatrixBridge(b MatrixBridge) error {
	if !id(b.ID) || endpoint(b.Address, true) != nil || !redisServerName(b.TLSServerName) || b.TLSCAFile != "" && !absolute(b.TLSCAFile) {
		return errors.New("matrix bridge requires a fixed verified TLS endpoint")
	}
	host, port, _ := net.SplitHostPort(b.Address)
	p, _ := strconv.Atoi(port)
	if net.ParseIP(host).IsUnspecified() || p == 0 {
		return errors.New("matrix bridge requires a concrete dial endpoint")
	}
	if !matrixwire.UserID(b.Account) || !absolute(b.AccessTokenFile) {
		return errors.New("matrix bridge requires explicit account and private access-token file")
	}
	if b.FreshnessSeconds < 1 || b.FreshnessSeconds > 300 {
		return errors.New("matrix bridge requires bounded freshness")
	}
	if err := c.validateBridgeRoutes("matrix", b.Routes, matrixwire.RoomID, false); err != nil {
		return err
	}
	return validateBridgeAuthors("matrix", b.GuestPrefix, b.Accounts, matrixwire.UserID, b.Account)
}
