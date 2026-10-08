package config

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
	"net"
	"slices"
	"strconv"
)

// MatrixBridge is an unused private provisioning prerequisite. It is not yet
// registered in service configuration or permitted to dial any endpoint.
type MatrixBridge struct {
	ID               string        `json:"id"`
	Address          string        `json:"address"`
	TLSServerName    string        `json:"tls_server_name"`
	TLSCAFile        string        `json:"tls_ca_file,omitempty"`
	Account          string        `json:"account"`
	AccessTokenFile  string        `json:"access_token_file"`
	FreshnessSeconds int64         `json:"freshness_seconds"`
	Routes           []MatrixRoute `json:"routes"`
}

type MatrixRoute struct {
	Author     string           `json:"author"`
	Room       string           `json:"room"`
	Principal  string           `json:"principal"`
	Target     core.Target      `json:"target"`
	Operations []core.Operation `json:"operations"`
}

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
	if b.FreshnessSeconds < 1 || b.FreshnessSeconds > 300 || len(b.Routes) < 1 || len(b.Routes) > 128 {
		return errors.New("matrix bridge requires bounded freshness and 1-128 grants")
	}
	grants := make(map[[2]string]bool, len(b.Routes))
	for _, r := range b.Routes {
		key := [2]string{r.Author, r.Room}
		if !matrixwire.UserID(r.Author) || !matrixwire.RoomID(r.Room) || r.Author == b.Account || !id(r.Principal) || grants[key] {
			return errors.New("invalid, duplicate or bridge-account Matrix grant")
		}
		grants[key] = true
		if len(r.Operations) == 0 || operations(r.Operations) != nil {
			return errors.New("matrix grant requires distinct explicit operations")
		}
		authorized := false
		for _, p := range c.Principals {
			if p.ID != r.Principal || p.Revoked || !slices.Contains(p.Targets, r.Target) {
				continue
			}
			authorized = true
			for _, op := range r.Operations {
				if !(op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search) || !slices.Contains(p.Operations, op) {
					return errors.New("matrix grant exceeds principal authority")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("matrix history/search requires a cursor key")
				}
			}
			break
		}
		if !authorized {
			return errors.New("matrix grant requires an active principal authorized for the exact target")
		}
	}
	return nil
}
