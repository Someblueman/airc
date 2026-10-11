//go:build darwin || linux

package adapter

import (
	"crypto/tls"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/testirc"
)

// ircOwnerFixture is f's bridge to a scripted IRC server that hands its
// registered connection to publish.
func ircOwnerFixture(t *testing.T, f bridgeAdapterFixture, publish func(*tls.Conn)) *auth.IRCServer {
	t.Helper()
	b := testirc.Serve(t, filepath.Dir(f.c.StateDir), publish)
	b.Routes = f.routes
	server, err := auth.NewIRCServer(f.c, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}
