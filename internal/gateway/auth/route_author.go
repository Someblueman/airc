package auth

import (
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// RouteAuthor is the AIRC author a provider-verified sender grant posts as:
// a configured bound author whose account token, if any, is held in memory
// like a client's, or a guest nickname derived from the provider identity.
// Provider verification (a pinned certificate, a signed-syslog origin, USM,
// a webhook HMAC or bearer) decides who reaches the grant; AIRC still decides
// whether the author may post.
type RouteAuthor struct {
	Author     core.Author
	Credential core.Credential
}

// Valid reports a usable author: a valid nickname, with a token exactly when
// registered.
func (a RouteAuthor) Valid() bool {
	return a.Author.Valid() && a.Author.Registered != a.Credential.Empty()
}

// LoadBoundAuthor reads a bound author's token file once. Without a token
// file the author is a guest under the configured nickname.
func LoadBoundAuthor(b config.BoundAuthor) (RouteAuthor, error) {
	a := RouteAuthor{Author: core.Author{Nick: b.Nick}}
	if !a.Author.Valid() {
		return RouteAuthor{}, ErrCredential
	}
	if b.AccountTokenFile == "" {
		return a, nil
	}
	token, err := ReadToken(b.AccountTokenFile)
	if err != nil {
		return RouteAuthor{}, ErrCredential
	}
	a.Credential, err = core.NewCredential(token)
	if err != nil {
		return RouteAuthor{}, ErrCredential
	}
	a.Author.Registered = true
	return a, nil
}
