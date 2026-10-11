package config

import (
	"errors"
	"fmt"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// PrincipalAuthor is the temporary principal shim: a configured principal
// posts as its binding's registered account, exactly as before per-job
// authors existed, and its credential still comes from the binding's token
// file. It disappears together with principals and bindings.
func (c Config) PrincipalAuthor(principal string) (core.Author, Binding, bool) {
	for _, p := range c.Principals {
		if p.ID != principal {
			continue
		}
		for _, b := range c.Bindings {
			if b.ID == p.Binding {
				author := core.Author{Nick: b.Account, Registered: true}
				return author, b, author.Valid()
			}
		}
	}
	return core.Author{}, Binding{}, false
}

// AuthorUpstream is the single AIRC dial template used for per-job authors:
// the configured upstream, or else the endpoint every binding shares. Author
// identity must not depend on which binding a listener happens to use, so
// without an explicit upstream it exists only when every binding names the
// same endpoint, TLS policy and access token; a binding's own account and
// token are never used for it.
func (c Config) AuthorUpstream() (Binding, bool) {
	if u := c.Upstream; u != nil {
		return Binding{ID: "upstream", Network: u.Network, Address: u.Address, AccessTokenFile: u.AccessTokenFile, GatewayTokenFile: u.GatewayTokenFile, TLSServerName: u.TLSServerName, TLSCAFile: u.TLSCAFile}, true
	}
	if len(c.Bindings) == 0 {
		return Binding{}, false
	}
	first := c.Bindings[0]
	for _, b := range c.Bindings[1:] {
		if b.Network != first.Network || b.Address != first.Address || b.AccessTokenFile != first.AccessTokenFile || b.TLSServerName != first.TLSServerName || b.TLSCAFile != first.TLSCAFile {
			return Binding{}, false
		}
	}
	return first, true
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
