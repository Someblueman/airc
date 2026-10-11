package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
)

// BoundAuthor is the AIRC author an ingress posts as when a request names
// none: a broker route, a verified syslog or SNMP sender, a mailbox account, a
// provider account map entry, or a local one-shot carrier (mailboxes, queues,
// shared memory, mapped files, offline imports) whose producers would
// otherwise keep tokens at rest. With an account token file the
// author is that registered account (the gateway holds its token, as a client
// would); without one it is a guest under that nickname. The gateway is not
// an identity authority: AIRC still decides whether the author may post.
type BoundAuthor struct {
	Nick             string `json:"nick"`
	AccountTokenFile string `json:"account_token_file,omitempty"`
}

// ErrRouteAuthor reports a route that names no usable author policy.
var ErrRouteAuthor = errors.New("route requires a bound_author or guests")

func (b *BoundAuthor) valid() bool {
	return b == nil || protocol.ValidNick(b.Nick) && (b.AccountTokenFile == "" || absolute(b.AccountTokenFile))
}

// protocolNick checks a required bound author.
func protocolNick(b BoundAuthor) bool { return b.valid() }

// RouteGuests reports whether a route admits authors named in the request
// body without an account token. The default is true for a route without a
// bound author and false for one with a bound author, so binding an author
// does not silently open the route to arbitrary guest nicknames.
func RouteGuests(bound *BoundAuthor, guests *bool) bool {
	if guests != nil {
		return *guests
	}
	return bound == nil
}

// validRouteAuthor checks a route's author policy: a valid bound author, or
// guests. A request naming a registered author with its token is accepted on
// any route, as on IRC; whoever the route's own gate lets in may name one.
func validRouteAuthor(bound *BoundAuthor, guests *bool) error {
	if !bound.valid() {
		return errors.New("invalid bound_author")
	}
	if bound == nil && !RouteGuests(bound, guests) {
		return ErrRouteAuthor
	}
	return nil
}

// boundAuthorFiles lists every configured bound author token file.
func (c Config) boundAuthorFiles() []string {
	var paths []string
	add := func(b *BoundAuthor) {
		if b != nil && b.AccountTokenFile != "" {
			paths = append(paths, b.AccountTokenFile)
		}
	}
	for _, b := range c.RedisPubSub {
		for _, r := range b.Routes {
			add(r.BoundAuthor)
		}
	}
	for _, b := range c.RedisStreams {
		add(b.Route.BoundAuthor)
	}
	for _, o := range c.PostgresNotify {
		add(o.Route.BoundAuthor)
	}
	for _, o := range c.SFTP {
		add(o.Route.BoundAuthor)
	}
	for _, l := range c.Listeners {
		add(l.BoundAuthor)
		if l.Syslog != nil {
			for _, r := range l.Syslog.Routes {
				add(&r.BoundAuthor)
			}
		}
		if l.SignedSyslog != nil {
			for _, r := range l.SignedSyslog.Routes {
				add(&r.BoundAuthor)
			}
		}
		if l.SNMP != nil {
			for _, r := range l.SNMP.Routes {
				add(&r.BoundAuthor)
			}
		}
		if l.SCPInbox != nil {
			add(l.SCPInbox.BoundAuthor)
		}
	}
	return paths
}
