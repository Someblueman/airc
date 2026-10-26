package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// validateLocalAuthor checks bound_author, for one-shot mailbox carriers
// whose producers would otherwise keep tokens at rest.
func (c Config) validateLocalAuthor(t Transport, l Listener) error {
	if l.BoundAuthor == nil {
		return nil
	}
	if !t.BoundAuthor {
		return errors.New("bound_author requires a mailbox listener")
	}
	if !(core.Author{Nick: l.BoundAuthor.Nick}).Valid() {
		return errors.New("bound_author requires a valid nickname")
	}
	if l.BoundAuthor.AccountTokenFile != "" && !absolute(l.BoundAuthor.AccountTokenFile) {
		return errors.New("bound_author: invalid account token file path")
	}
	return nil
}
