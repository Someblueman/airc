package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// detachedCredentials names the author a fixture's plain requests carry.
type detachedCredentials struct {
	nick, secret string
}

// provisionDetached returns the fixture author's request writer. The
// configuration needs nothing: requests name their author.
func provisionDetached(t *testing.T, _ *config.Config) *detachedCredentials {
	t.Helper()
	return &detachedCredentials{nick: fixtureAuthor, secret: fixtureAccount}
}

// token is r as a plain contract request naming the author; encrypted is
// ignored (there is one request profile).
func (c *detachedCredentials) token(t *testing.T, r core.Request, _ bool) string {
	t.Helper()
	return authorRequest(t, r, c.nick, c.secret)
}

// validityToken is token with the request's own validity window.
func (c *detachedCredentials) validityToken(t *testing.T, r core.Request, issued, expiry int64, _ bool) string {
	t.Helper()
	r.IssuedAt, r.ExpiresAt = &issued, &expiry
	return authorRequest(t, r, c.nick, c.secret)
}
