package service

import (
	"context"
	"crypto/x509"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/couchdbwire"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type couchDBChangesOwner struct {
	options    config.CouchDBChanges
	binding    string
	client     *couchdbwire.Client
	dispatcher *dispatch.Dispatcher
}

func (o *couchDBChangesOwner) close() {
	if o.client != nil {
		o.client.Close()
	}
}

// prepareCouchDBChanges pins local progress and loads explicit credentials.
// It neither contacts CouchDB nor provisions a database or its access policy.
func prepareCouchDBChanges(ctx context.Context, c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*couchDBChangesOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.close()
			}
			owners = nil
		}
	}()
	for _, options := range c.CouchDBChanges {
		if !options.Enabled {
			continue
		}
		o := &couchDBChangesOwner{options: options}
		owners = append(owners, o)
		if o.options.InitialSequence == "" {
			o.options.InitialSequence = "0"
		}
		for _, principal := range c.Principals {
			if principal.ID == options.Route.Principal && !principal.Revoked {
				o.binding = principal.Binding
				break
			}
		}
		if o.binding == "" {
			return owners, auth.ErrCredential
		}
		var roots *x509.CertPool
		if options.TLSCAFile != "" {
			roots, err = auth.ReadTrustRoots(options.TLSCAFile)
			if err != nil {
				return owners, err
			}
		}
		password, passwordErr := auth.ReadCouchDBPassword(options.PasswordFile)
		if passwordErr != nil {
			return owners, passwordErr
		}
		o.client, err = couchdbwire.NewClient(options.Endpoint, options.Database, options.Username, password, roots)
		clear(password)
		if err != nil {
			return owners, err
		}
		o.dispatcher, err = dispatch.New(s, v, b, cursors, []core.Operation{core.Send})
		if err != nil {
			return owners, err
		}
		prepareCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = s.ProvisionCouchDBProgress(prepareCtx, options.Namespace, config.CouchDBChangesScope(c.Realm, o.binding, o.options), o.options.InitialSequence)
		cancel()
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
