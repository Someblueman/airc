//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// oscoreRPCFixture is a native OSCORE bridge's dispatcher and one plain
// correlated frame naming the fixture's guest author, shared by the
// file-backed local carriers that take the same frame.
func oscoreRPCFixture(t *testing.T) (plainFixture, []byte, auth.OSCOREOwner) {
	t.Helper()
	f := newPlainFixture(t, config.Listener{ID: "oscore", Protocol: "coap_oscore", Operations: []core.Operation{core.Send}})
	request := core.Request{Version: 1, RequestID: "oscore-rpc-message", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "first\r\nsecond"}
	body := []byte(`{"version":1,"correlation_id":"native-rpc","profile":"plain","request":` + f.body(t, request) + `}`)
	return f, body, auth.OSCOREOwner{Realm: f.c.Realm, Listener: "oscore"}
}

// ingress is a fresh per-connection author ingress for f's dispatcher.
func (f plainFixture) ingress() *dispatch.Ingress {
	return &dispatch.Ingress{Session: dispatch.NewSession()}
}
