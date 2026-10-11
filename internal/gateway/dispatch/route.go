package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// RouteAuthor is a route-owned ingress's author policy. The route's own gate
// (a broker ACL, a database role, an SSH login, a pinned sender) decides who
// may use it; RouteAuthor decides who they post as: the author a request
// names, else the route's bound author. A request naming a registered author
// with its token is always accepted, as on IRC; one naming a guest only when
// the route allows guests.
type RouteAuthor struct {
	bound  *auth.RouteAuthor
	guests bool
}

// NewRouteAuthor loads a route's bound author, if any, and its guest policy.
func NewRouteAuthor(bound *config.BoundAuthor, guests bool) (RouteAuthor, error) {
	r := RouteAuthor{guests: guests}
	if bound != nil {
		a, err := auth.LoadBoundAuthor(*bound)
		if err != nil {
			return RouteAuthor{}, err
		}
		r.bound = &a
	}
	if r.bound == nil && !guests {
		return RouteAuthor{}, config.ErrRouteAuthor
	}
	return r, nil
}

// resolve picks the author for one request. A request with no author on a
// route without a bound author is anonymous, which admits only discovery.
func (r RouteAuthor) resolve(sub core.Submission) (core.Author, core.Credential, error) {
	if sub.Author != nil {
		if !sub.Author.Registered && !r.guests {
			return core.Author{}, core.Credential{}, auth.ErrForbidden
		}
		return *sub.Author, sub.Credential, nil
	}
	if r.bound != nil {
		return r.bound.Author, r.bound.Credential, nil
	}
	return core.Author{}, core.Credential{}, nil
}

// ListenerID is the listener or route this dispatcher admits for.
func (d *Dispatcher) ListenerID() string { return d.listener() }

// RouteSource labels a route for AIRC's per-source limits: "route:<id>".
func RouteSource(id string) string { return "route:" + id }

// ForRoute constructs the dispatcher of a route-owned ingress that is not a
// listener (a Redis or PostgreSQL route, an SFTP mailbox). It carries the
// route's operations and IRC-style flood limits; guests and passwords are
// the route's and its broker's business, never a listener policy.
func ForRoute(journal *store.Store, reader *backend.Backend, cursors *auth.CursorCodec, id string, operations []core.Operation) (*Dispatcher, error) {
	d, err := newDispatcher(journal, reader, cursors, operations)
	if err != nil {
		return nil, err
	}
	g := &gate{listener: id, guests: true}
	for _, op := range ImplementedOperations() {
		if slices.Contains(operations, op) && !((op == core.HistoryRead || op == core.Search || op.IsWatch()) && cursors == nil) {
			g.operations = append(g.operations, op)
		}
	}
	g.operations = append(g.operations, core.CapabilitiesGet)
	g.perSource = newBuckets(config.DefaultRequestsPerMinute)
	g.perAuthor = newBuckets(config.DefaultAuthorRequestsPerMinute)
	d.gate = g
	return d, nil
}

// SubmitRoute decodes a plain contract request that arrived on a route and
// runs it as the route's author. The request's "password" member, if any, is
// ignored: routes have no listener password.
func (d *Dispatcher) SubmitRoute(ctx context.Context, route RouteAuthor, source string, body []byte, now time.Time) (DetachedResult, error) {
	sub, err := core.DecodeSubmission(body)
	if err != nil {
		return DetachedResult{}, store.ErrInvalid
	}
	return d.SubmitRouteDecoded(ctx, route, source, sub, now)
}

// SubmitRouteDecoded is SubmitRoute for a carrier that inspects the decoded
// request (its target or operation) before admission.
func (d *Dispatcher) SubmitRouteDecoded(ctx context.Context, route RouteAuthor, source string, sub core.Submission, now time.Time) (DetachedResult, error) {
	author, credential, err := route.resolve(sub)
	if err != nil {
		return DetachedResult{}, err
	}
	return d.Admit(ctx, d.listener(), source, author, credential, sub.Request, now)
}

// AdmitAs runs a request the gateway built from a provider-verified message
// (a syslog record) as that sender grant's author.
func (d *Dispatcher) AdmitAs(ctx context.Context, source string, a auth.RouteAuthor, r core.Request, now time.Time) (DetachedResult, error) {
	if !a.Valid() {
		return DetachedResult{}, auth.ErrAuthentication
	}
	return d.Admit(ctx, d.listener(), source, a.Author, a.Credential, r, now)
}

// Connect applies a listener's access password to a new connection, as IRC's
// PASS before registration, for carriers whose login exchange carries it (an
// MQTT CONNECT password, AMQP SASL PLAIN, a STOMP passcode). It is never an
// identity. On a listener without a password every connection is admitted.
func (d *Dispatcher) Connect(s *Session, password core.Password) error {
	if d == nil || d.gate == nil || s == nil {
		return ErrUnsupported
	}
	if g := d.gate; g.password != nil && !password.Matches(*g.password) {
		return auth.ErrForbidden
	}
	s.admit()
	return nil
}

// routeActor applies the ingress's policy (operations, flood limits) to a
// provider-verified author and returns its journal submission.
func (d *Dispatcher) routeActor(source string, a auth.RouteAuthor, r core.Request, now time.Time) (store.Submission, error) {
	if !a.Valid() {
		return store.Submission{}, auth.ErrAuthentication
	}
	who, _, err := d.admit(d.listener(), source, a.Author, a.Credential, r, now)
	if err != nil {
		return store.Submission{}, err
	}
	return *who.author, nil
}

// PasswordRequired reports whether the listener has an access password.
func (d *Dispatcher) PasswordRequired() bool {
	return d != nil && d.gate != nil && d.gate.password != nil
}
