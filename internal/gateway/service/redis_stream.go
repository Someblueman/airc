package service

import (
	"crypto/tls"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type redisStreamOwner struct {
	options    config.RedisStreams
	security   *tls.Config
	password   []byte
	dispatcher *dispatch.Dispatcher
}

func (o *redisStreamOwner) clear() { clear(o.password) }

// prepareRedisStreams prepares local credentials and authority without dialing.
func prepareRedisStreams(c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*redisStreamOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.clear()
			}
			owners = nil
		}
	}()
	for _, options := range c.RedisStreams {
		if !options.Enabled {
			continue
		}
		o := &redisStreamOwner{options: options, security: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: options.TLSServerName}}
		owners = append(owners, o)
		o.options.Route.Operations = slices.Clone(options.Route.Operations)
		if options.TLSCAFile != "" {
			o.security.RootCAs, err = auth.ReadTrustRoots(options.TLSCAFile)
			if err != nil {
				return owners, err
			}
		}
		o.password, err = auth.ReadRedisPassword(options.Credential.PasswordFile)
		if err != nil {
			return owners, err
		}
		o.dispatcher, err = dispatch.New(s, v, b, cursors, o.options.Route.Operations)
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
