package service

import (
	"crypto/tls"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type redisOwner struct {
	options               config.RedisPubSub
	security              *tls.Config
	subscriber, publisher []byte
	dispatcher            *dispatch.Dispatcher
	sealer                *auth.ResponseSealer
}

func (o *redisOwner) clear() { clear(o.subscriber); clear(o.publisher) }

// prepareRedis never dials. All credentials, trust and route dispatchers are
// prepared before Ready; the service clears passwords after owners join.
func prepareRedis(c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*redisOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.clear()
			}
			owners = nil
		}
	}()
	for _, options := range c.RedisPubSub {
		if !options.Enabled {
			continue
		}
		o := &redisOwner{options: options, security: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: options.TLSServerName}}
		owners = append(owners, o)
		o.options.Routes = slices.Clone(options.Routes)
		var operations []core.Operation
		for i, r := range o.options.Routes {
			o.options.Routes[i].Operations = slices.Clone(r.Operations)
			for _, op := range r.Operations {
				if !slices.Contains(operations, op) {
					operations = append(operations, op)
				}
			}
		}
		if options.TLSCAFile != "" {
			o.security.RootCAs, err = auth.ReadTrustRoots(options.TLSCAFile)
			if err != nil {
				return owners, err
			}
		}
		o.subscriber, err = auth.ReadRedisPassword(options.Subscriber.PasswordFile)
		if err != nil {
			return owners, err
		}
		o.publisher, err = auth.ReadRedisPassword(options.Publisher.PasswordFile)
		if err != nil {
			return owners, err
		}
		o.sealer, err = loadSignedResponseSigner(c)
		if err != nil {
			return owners, err
		}
		o.dispatcher, err = dispatch.New(s, v, b, cursors, operations)
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
