package service

import (
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type sftpOwner struct {
	options     config.SFTP
	author      dispatch.RouteAuthor
	credentials auth.SFTPCredentials
	dispatcher  *dispatch.Dispatcher
}

func (o *sftpOwner) close() {
	o.credentials.Close()
}

// prepareSFTP loads only local provisioning. It neither contacts nor configures
// an SSH server. Run joins mailbox owners before clearing their credentials.
func prepareSFTP(c config.Config, s *store.Store, b *backend.Backend, cursors *auth.CursorCodec) (owners []*sftpOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.close()
			}
			owners = nil
		}
	}()
	for _, options := range c.SFTP {
		if !options.Enabled {
			continue
		}
		o := &sftpOwner{options: options}
		owners = append(owners, o)
		o.options.Route.Operations = slices.Clone(options.Route.Operations)
		route := options.Route
		if o.author, err = dispatch.NewRouteAuthor(route.BoundAuthor, config.RouteGuests(route.BoundAuthor, route.Guests)); err != nil {
			return owners, err
		}
		o.credentials, err = auth.ReadSFTPCredentials(options.PrivateKeyFile, options.HostKeyFile)
		if err != nil {
			return owners, err
		}
		o.dispatcher, err = dispatch.ForRoute(s, b, cursors, options.ID, options.Route.Operations)
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
