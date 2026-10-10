package service

import (
	"crypto/ecdsa"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/sqswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type sqsOwner struct {
	options    config.SQS
	client     *sqswire.Client
	dispatcher *dispatch.Dispatcher
	sealer     *auth.ResponseSealer
	recipient  *ecdsa.PublicKey
}

func (o *sqsOwner) close() {
	if o.client != nil {
		o.client.Close()
	}
}

// prepareSQS loads explicit local authority and credentials without AWS calls.
// Runtime queue checks precede intake; no queue or IAM resource is provisioned.
func prepareSQS(c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*sqsOwner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.close()
			}
			owners = nil
		}
	}()
	for _, options := range c.SQS {
		if !options.Enabled {
			continue
		}
		o := &sqsOwner{options: options}
		owners = append(owners, o)
		o.options.Route.Operations = slices.Clone(options.Route.Operations)
		credentials, e := auth.ReadSQSCredentials(options.CredentialFile)
		if e != nil {
			return owners, e
		}
		o.client, err = sqswire.NewClient(options.Region, credentials)
		clear(credentials.SecretAccessKey)
		clear(credentials.SessionToken)
		if err != nil {
			return owners, err
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
	}
	return owners, nil
}
