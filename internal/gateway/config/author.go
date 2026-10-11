package config

import (
	"errors"
	"fmt"
)

// AuthorUpstream is the single AIRC endpoint every author session dials.
// Author identity never depends on the listener, so there is exactly one.
func (c Config) AuthorUpstream() (Upstream, bool) {
	if c.Upstream == nil {
		return Upstream{}, false
	}
	return *c.Upstream, true
}

func (c Config) validateUpstream() error {
	u := c.Upstream
	if u == nil {
		return nil
	}
	if u.AccessTokenFile != "" && !absolute(u.AccessTokenFile) || u.GatewayTokenFile != "" && !absolute(u.GatewayTokenFile) || u.TLSCAFile != "" && !absolute(u.TLSCAFile) {
		return errors.New("upstream: invalid credential path")
	}
	if u.GatewayTokenFile != "" && u.GatewayTokenFile == u.AccessTokenFile {
		return errors.New("upstream: gateway and access credentials must differ")
	}
	switch u.Network {
	case "unix":
		if !absolute(u.Address) || u.TLSServerName != "" || u.TLSCAFile != "" {
			return errors.New("unix upstream requires an absolute socket and no TLS settings")
		}
	case "tcp":
		if err := endpoint(u.Address, u.TLSServerName != ""); err != nil {
			return fmt.Errorf("upstream: %w", err)
		}
		if u.TLSServerName == "" && u.TLSCAFile != "" {
			return errors.New("upstream: TLS CA requires a server name")
		}
		if u.TLSServerName != "" && u.AccessTokenFile == "" {
			return errors.New("upstream: TLS upstream requires an access token file")
		}
	default:
		return errors.New("upstream: unsupported network")
	}
	return nil
}
