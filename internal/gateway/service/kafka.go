package service

import (
	"context"
	"crypto/tls"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type kafkaOwner struct {
	options    config.Kafka
	security   map[int32]*tls.Config
	password   []byte
	dispatcher *dispatch.Dispatcher
	sealer     *auth.ResponseSealer
}

func (o *kafkaOwner) clear() { clear(o.password) }

// prepareKafka validates local credentials and pins journal ownership without
// dialing. A preparation failure clears all already loaded passwords.
func prepareKafka(ctx context.Context, c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*kafkaOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.clear()
			}
			owners = nil
		}
	}()
	for _, options := range c.Kafka {
		if !options.Enabled {
			continue
		}
		o := &kafkaOwner{options: options, security: make(map[int32]*tls.Config, len(options.Brokers))}
		owners = append(owners, o)
		o.options.Brokers = slices.Clone(options.Brokers)
		o.options.Route.Operations = slices.Clone(options.Route.Operations)
		initial := *options.Route.InitialOffset
		o.options.Route.InitialOffset = &initial
		for _, broker := range options.Brokers {
			security := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: broker.TLSServerName}
			if broker.TLSCAFile != "" {
				security.RootCAs, err = auth.ReadTrustRoots(broker.TLSCAFile)
				if err != nil {
					return owners, err
				}
			}
			o.security[broker.ID] = security
		}
		// This reader preserves exact private-file bytes; PLAIN validation below
		// adds Kafka's UTF-8 and non-NUL constraints without trimming the secret.
		o.password, err = auth.ReadRedisPassword(options.Credential.PasswordFile)
		if err != nil {
			return owners, err
		}
		body, validationErr := kafkawire.PlainAuthenticate(options.Credential.Username, o.password)
		clear(body)
		if validationErr != nil {
			return owners, auth.ErrCredential
		}
		o.sealer, err = loadSignedResponseSigner(c)
		if err != nil {
			return owners, err
		}
		o.dispatcher, err = dispatch.New(s, v, b, cursors, o.options.Route.Operations)
		if err != nil {
			return owners, err
		}
		provision, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err = s.ProvisionKafka(provision, options.ID)
		cancel()
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
