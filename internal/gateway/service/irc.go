package service

import (
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type ircOwner struct {
	id         string
	server     *auth.IRCServer
	dispatcher *dispatch.Dispatcher
}

// prepareIRC does not dial. Credentials and immutable grants are prepared before
// Ready. Run joins every owner before closing servers and clearing passwords.
func prepareIRC(c config.Config, s *store.Store, v *auth.Verifier) (owners []*ircOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.server.Close()
			}
			owners = nil
		}
	}()
	for _, options := range c.IRCBridges {
		if !options.Enabled {
			continue
		}
		server, err := auth.NewIRCServer(v, c, options)
		if err != nil {
			return owners, err
		}
		o := &ircOwner{id: options.ID, server: server}
		owners = append(owners, o)
		o.dispatcher, err = dispatch.New(s, v, nil, nil, []core.Operation{core.Send})
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
