package dispatch

import "github.com/Someblueman/airc/internal/gateway/core"

// NewBoundSession is a session whose default author is fixed by the carrier's
// own configuration (such as an OSCORE security context), as if it had
// identified: requests without an author post as this one, and AIRC still
// checks it on every request. An account.identify on the session replaces it.
func NewBoundSession(author core.Author, credential core.Credential) *Session {
	s := NewSession()
	if author.Valid() && author.Registered != credential.Empty() {
		s.identify(author, credential)
	}
	return s
}
