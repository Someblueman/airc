package service

import (
	"crypto/ed25519"
	"crypto/tls"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type postgresNotifyOwner struct {
	options    config.PostgresNotify
	author     dispatch.RouteAuthor
	security   *tls.Config
	dispatcher *dispatch.Dispatcher
}

func (o *postgresNotifyOwner) close() {
	if o.security == nil {
		return
	}
	for i := range o.security.Certificates {
		if key, ok := o.security.Certificates[i].PrivateKey.(ed25519.PrivateKey); ok {
			clear(key)
		}
		o.security.Certificates[i].PrivateKey = nil
	}
}

// preparePostgresNotify loads only local certificate and native authority.
// It does not connect, create a role or change PostgreSQL authentication policy.
func preparePostgresNotify(c config.Config, s *store.Store, b *backend.Backend, cursors *auth.CursorCodec) (owners []*postgresNotifyOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.close()
			}
			owners = nil
		}
	}()
	for _, options := range c.PostgresNotify {
		if !options.Enabled {
			continue
		}
		o := &postgresNotifyOwner{options: options, security: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: options.TLSServerName}}
		owners = append(owners, o)
		route := options.Route
		if o.author, err = dispatch.NewRouteAuthor(route.BoundAuthor, config.RouteGuests(route.BoundAuthor, route.Guests)); err != nil {
			return owners, err
		}
		if options.TLSCAFile != "" {
			o.security.RootCAs, err = auth.ReadTrustRoots(options.TLSCAFile)
			if err != nil {
				return owners, err
			}
		}
		certificate, certificateErr := auth.ReadPostgresClientCertificate(options.ClientCertFile, options.ClientKeyFile, options.Username)
		if certificateErr != nil {
			return owners, certificateErr
		}
		o.security.Certificates = []tls.Certificate{certificate}
		o.dispatcher, err = dispatch.ForRoute(s, b, cursors, options.ID, []core.Operation{core.Send})
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
