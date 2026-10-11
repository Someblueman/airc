//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// stompTestAuthor is the guest every STOMP fixture request names.
const stompTestAuthor = "stomp-guest"

// stompAuthorFixture is a STOMP listener's author dispatcher over a real
// journal; requests name their authors.
type stompAuthorFixture struct {
	*routeFixture
	d  *dispatch.Dispatcher
	in dispatch.Ingress
}

func newSTOMPAuthorFixture(t *testing.T) stompAuthorFixture {
	t.Helper()
	f := newRouteFixture(t)
	return stompAuthorFixture{routeFixture: f, d: f.listener(t, stompFixtureListener()), in: dispatch.Ingress{Source: "192.0.2.1", Session: dispatch.NewSession()}}
}

func stompFixtureRoute() config.STOMPRoute {
	r := amqpFixtureRoute()
	return config.STOMPRoute{RequestDestination: r.RequestAddress, ResponseDestination: r.ResponseAddress, Target: r.Target, Operations: r.Operations}
}

func stompFixtureListener() config.Listener {
	r := stompFixtureRoute()
	return config.Listener{ID: "stomp", Protocol: "stomp", Operations: r.Operations, STOMP: &config.STOMP{Routes: []config.STOMPRoute{r}}}
}

// lookup reads a job of the fixture's guest author.
func (f stompAuthorFixture) lookup(t *testing.T, requestID string) (store.Job, error) {
	t.Helper()
	return f.s.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: stompTestAuthor}}, requestID)
}
