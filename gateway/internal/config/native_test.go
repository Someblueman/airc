package config

import (
	"strings"
	"testing"
)

// A native tunnel needs the gateway credential: without WEBIRC, aircd would
// see every tunnelled client as the gateway's own local peer.
func TestNativeCarriersRequireTheGatewayCredential(t *testing.T) {
	for _, protocol := range []string{"irc_websocket", "irc_connect"} {
		c := udpConfig()
		c.Listeners[0] = Listener{ID: "native", Protocol: protocol, Enabled: true, Address: "0.0.0.0:6690"}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "gateway_token_file") {
			t.Fatalf("%s without a gateway credential: %v", protocol, err)
		}
		c.Upstream.GatewayTokenFile = "/tmp/gateway.token"
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		c.Upstream.GatewayTokenFile, c.Listeners[0].Enabled = "", false
		if err := c.Validate(); err != nil {
			t.Fatalf("disabled %s: %v", protocol, err)
		}
	}
}
