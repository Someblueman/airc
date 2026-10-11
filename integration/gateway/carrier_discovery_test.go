//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// A client reaching the gateway reads the same ordered carrier list aircd
// advertises in 005 CARRIERS, with no credential.
func TestCapabilitiesPublishCarriers(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "web", Protocol: "http"})
	c.Carriers = []string{"irc:6667", "framed:6683", "http:6680"}
	g := startAuthorGateway(t, c)
	var capabilities struct {
		Carriers []string `json:"carriers"`
	}
	if status := plainHTTP(t, http.MethodGet, "http://"+g.addresses["web"]+"/v1/capabilities", nil, nil, &capabilities); status != http.StatusOK || !slices.Equal(capabilities.Carriers, c.Carriers) {
		t.Fatalf("HTTP %d carriers %v", status, capabilities.Carriers)
	}
}
