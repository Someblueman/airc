//go:build darwin || linux

package service

import (
	"crypto/tls"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testirc"
)

// ircServiceFixture is a gateway whose one IRC bridge connects to a scripted
// server that hands its registered connection to publish.
func ircServiceFixture(t *testing.T, publish func(*tls.Conn)) config.Config {
	t.Helper()
	c := upstreamServiceConfig(t)
	c.Listeners = []config.Listener{}
	b := testirc.Serve(t, filepath.Dir(c.StateDir), publish)
	b.Enabled = true
	b.Routes = []config.IRCRoute{{Room: "#external", Target: core.Target{Kind: "room", Name: "#fixture"}, Operations: []core.Operation{core.Send}}}
	c.IRCBridges = []config.IRCBridge{b}
	return c
}
