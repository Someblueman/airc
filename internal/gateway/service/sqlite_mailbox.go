package service

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/sqlitemailbox"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type sqliteMailboxOwner struct {
	options    config.SQLiteMailbox
	binding    string
	mailbox    *sqlitemailbox.Mailbox
	dispatcher *dispatch.Dispatcher
	sealer     *auth.ResponseSealer
	recipient  *ecdsa.PublicKey
}

func (o *sqliteMailboxOwner) close() error {
	o.sealer.Close()
	if o.mailbox != nil {
		return o.mailbox.Close()
	}
	return nil
}

// prepareSQLiteMailboxes opens only explicitly provisioned local databases.
// Run joins every owner before closing its database and response signer.
func prepareSQLiteMailboxes(ctx context.Context, c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*sqliteMailboxOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				err = errors.Join(err, o.close())
			}
			owners = nil
		}
	}()
	for _, options := range c.SQLiteMailboxes {
		if !options.Enabled {
			continue
		}
		o := &sqliteMailboxOwner{options: options}
		owners = append(owners, o)
		o.options.Route.Operations = slices.Clone(options.Route.Operations)
		for _, principal := range c.Principals {
			if principal.ID == options.Route.Principal && !principal.Revoked {
				o.binding = principal.Binding
				break
			}
		}
		if o.binding == "" {
			return owners, auth.ErrCredential
		}
		o.recipient, err = auth.ReadResponseRecipientKey(options.Route.RecipientPublicKeyFile)
		if err != nil {
			return owners, err
		}
		o.sealer, err = loadSignedResponseSigner(c)
		if err != nil {
			return owners, err
		}
		o.dispatcher, err = dispatch.New(s, v, b, cursors, options.Route.Operations)
		if err != nil {
			return owners, err
		}
		openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		o.mailbox, err = sqlitemailbox.Open(openCtx, options.Directory)
		cancel()
		if err != nil {
			return owners, err
		}
	}
	return owners, nil
}
