package fallback

import (
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

// Native IRC through a real tunnel (a post, its retry, a lookup, a history
// read and discovery over irc_websocket and irc_connect) is tested in the
// gateway module, which serves one in process: gateway/integration,
// client_native_tunnel_test.go.

// The gateways view of a client leaves native tunnels out: they are where
// native IRC runs, not where rooms are polled with gateway requests.
func TestNativeTunnelsAreNotGatewayEndpoints(t *testing.T) {
	c := &Client{Endpoints: []Endpoint{ircEndpoint(6667), {Carrier: protocol.Carrier{Name: "irc_websocket", Port: 6689}}, {Carrier: protocol.Carrier{Name: "irc_connect", Port: 6690}}, {Carrier: protocol.Carrier{Name: "http", Port: 6680}}}}
	if got := c.Gateways().Endpoints; len(got) != 1 || got[0].Name != "http" {
		t.Fatalf("gateway endpoints %v", got)
	}
	for _, name := range []string{"irc", "irc_websocket", "irc_connect"} {
		if !NativeCarrier(name) || !Supported(name) {
			t.Errorf("%s is not a supported native carrier", name)
		}
	}
	if NativeCarrier("websocket") {
		t.Error("the contract WebSocket carrier is not native")
	}
}
