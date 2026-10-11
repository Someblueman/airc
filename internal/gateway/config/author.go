package config

import "github.com/Someblueman/airc/internal/gateway/core"

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

// Upstream is the single AIRC dial template used for per-job authors. Author
// identity must not depend on which binding a listener happens to use, so it
// exists only when every binding names the same endpoint, TLS policy and
// access token; the binding's own account and token are never used for it.
func (c Config) Upstream() (Binding, bool) {
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
