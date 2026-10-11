package service

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// bridgeOwner is one enabled chat bridge: its verified server and the
// dispatcher for its routes.
type bridgeOwner[S interface{ Close() }] struct {
	id         string
	server     S
	dispatcher *dispatch.Dispatcher
	run        func(context.Context, S, *dispatch.Dispatcher, chan struct{}, chan struct{}) error
}

func (o *bridgeOwner[S]) bridgeID() string { return o.id }
func (o *bridgeOwner[S]) release()         { o.server.Close() }
func (o *bridgeOwner[S]) serve(r *startup) error {
	return o.run(r.work, o.server, o.dispatcher, r.slots, r.requests)
}

// prepareIRC does not dial. Credentials and immutable grants are prepared before
// Ready. Run joins every owner before closing servers and clearing passwords.
func prepareIRC(c config.Config, s *store.Store, b *backend.Backend, cursors *auth.CursorCodec) ([]*bridgeOwner[*auth.IRCServer], error) {
	return prepareBridges(s, b, cursors, c.IRCBridges,
		func(o config.IRCBridge) (string, bool, []config.BridgeRoute) { return o.ID, o.Enabled, o.Routes },
		func(o config.IRCBridge) (*auth.IRCServer, error) { return auth.NewIRCServer(c, o) }, adapter.ServeIRC)
}

// prepareXMPP validates immutable grants and credentials without dialing. Run
// starts owners after preparation and joins them before clearing credentials.
func prepareXMPP(c config.Config, s *store.Store, b *backend.Backend, cursors *auth.CursorCodec) ([]*bridgeOwner[*auth.XMPPServer], error) {
	return prepareBridges(s, b, cursors, c.XMPPBridges,
		func(o config.XMPPBridge) (string, bool, []config.BridgeRoute) { return o.ID, o.Enabled, o.Routes },
		func(o config.XMPPBridge) (*auth.XMPPServer, error) { return auth.NewXMPPServer(c, o) }, adapter.ServeXMPP)
}

// prepareBridges opens a server and a dispatcher for each enabled bridge. On
// failure it closes every server it opened.
func prepareBridges[B any, S interface{ Close() }](s *store.Store, b *backend.Backend, cursors *auth.CursorCodec, bridges []B, spec func(B) (id string, enabled bool, routes []config.BridgeRoute), open func(B) (S, error), run func(context.Context, S, *dispatch.Dispatcher, chan struct{}, chan struct{}) error) (owners []*bridgeOwner[S], err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.server.Close()
			}
			owners = nil
		}
	}()
	for _, options := range bridges {
		id, enabled, routes := spec(options)
		if !enabled {
			continue
		}
		server, err := open(options)
		if err != nil {
			return owners, err
		}
		o := &bridgeOwner[S]{id: id, server: server, run: run}
		owners = append(owners, o)
		o.dispatcher, err = dispatch.ForBridge(s, b, cursors, id, routes)
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
