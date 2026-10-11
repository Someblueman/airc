package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// journalScope is the author scope the principal shim keys a principal's jobs by.
func journalScope(t *testing.T, s *store.Store, identity auth.Identity) string {
	t.Helper()
	scope, err := s.AuthorScope(identity)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

// principalScope is the cursor owner of a configured shim principal.
func principalScope(c config.Config, principal string) string {
	author, _, _ := c.PrincipalAuthor(principal)
	return author.Scope()
}
