//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// amqpAuthorFixture is an AMQP listener's author dispatcher over a real
// journal; requests name their authors.
type amqpAuthorFixture struct {
	*routeFixture
	d  *dispatch.Dispatcher
	in dispatch.Ingress
}

func newAMQPAuthorFixture(t *testing.T) amqpAuthorFixture {
	t.Helper()
	f := newRouteFixture(t)
	return amqpAuthorFixture{routeFixture: f, d: f.listener(t, amqpFixtureListener()), in: dispatch.Ingress{Source: "192.0.2.1", Session: dispatch.NewSession()}}
}

// amqpAuthorRequest is a guest's plain contract request.
func amqpAuthorRequest() map[string]any {
	return map[string]any{"version": 1, "request_id": "amqp-stable-request", "operation": "send", "target": map[string]string{"kind": "room", "name": "#fixture"}, "message": "exact body\n世界", "author": map[string]string{"nick": "amqp-guest"}}
}

// body wraps a plain contract request in one complete data-section delivery.
func (f amqpAuthorFixture) body(t *testing.T, r any) *amqpIncoming {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return amqpIncomingBody(t, body)
}

func (f amqpAuthorFixture) route() config.AMQPRoute { return amqpFixtureRoute() }
