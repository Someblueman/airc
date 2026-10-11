package service

import (
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type xmppOwner struct {
	id         string
	server     *auth.XMPPServer
	dispatcher *dispatch.Dispatcher
}

// prepareXMPP validates immutable grants and credentials without dialing. Run
// starts owners after preparation and joins them before clearing credentials.
func prepareXMPP(c config.Config, s *store.Store, b *backend.Backend, cursors *auth.CursorCodec) (owners []*xmppOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.server.Close()
			}
			owners = nil
		}
	}()
	for _, options := range c.XMPPBridges {
		if !options.Enabled {
			continue
		}
		server, err := auth.NewXMPPServer(c, options)
		if err != nil {
			return owners, err
		}
		o := &xmppOwner{id: options.ID, server: server}
		owners = append(owners, o)
		o.dispatcher, err = dispatch.ForBridge(s, b, cursors, options.ID, options.Routes)
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
