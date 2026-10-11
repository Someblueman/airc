package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// validateLocalAuthor checks bound_author, for one-shot carriers whose
// producers would otherwise keep tokens at rest, and reply_to, the one
// owner-provisioned reply destination of packet carriers whose producers have
// no address of their own.
func (c Config) validateLocalAuthor(t Transport, l Listener) error {
	if l.BoundAuthor != nil {
		if !t.BoundAuthor {
			return errors.New("bound_author requires a mailbox, queue, shared memory, mapped file or offline listener")
		}
		if !(core.Author{Nick: l.BoundAuthor.Nick}).Valid() {
			return errors.New("bound_author requires a valid nickname")
		}
		if l.BoundAuthor.AccountTokenFile != "" && !absolute(l.BoundAuthor.AccountTokenFile) {
			return errors.New("bound_author: invalid account token file path")
		}
	}
	if l.ReplyTo == "" {
		if l.Enabled && t.replyTo != nil {
			return errors.New("a queue, shared memory or mapped file listener requires reply_to")
		}
		return nil
	}
	if t.replyTo == nil {
		return errors.New("reply_to requires a POSIX queue, System V queue, shared memory or mapped file listener")
	}
	if !t.replyTo(c, l) {
		return errors.New("reply_to must name a distinct destination of the listener's own kind")
	}
	return nil
}
