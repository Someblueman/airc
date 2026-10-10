package service

import (
	"crypto/ecdsa"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/s3wire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type s3Owner struct {
	options    config.S3
	binding    string
	client     *s3wire.Client
	dispatcher *dispatch.Dispatcher
	sealer     *auth.ResponseSealer
	recipient  *ecdsa.PublicKey
}

func (o *s3Owner) close() {
	if o.client != nil {
		o.client.Close()
	}
	o.sealer.Close()
}

// prepareS3 loads only local provisioning. It neither contacts nor configures
// an S3 bucket. Run joins mailbox owners before clearing their credentials.
func prepareS3(c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*s3Owner, err error) {
	defer func() {
		if err != nil {
			for _, o := range owners {
				o.close()
			}
			owners = nil
		}
	}()
	for _, options := range c.S3 {
		if !options.Enabled {
			continue
		}
		o := &s3Owner{options: options}
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
		credentials, credentialErr := auth.ReadAWSCredentials(options.CredentialFile)
		if credentialErr != nil {
			return owners, credentialErr
		}
		o.client, err = s3wire.NewClient(options.Region, options.Bucket, options.AccountID, options.Prefix, s3wire.Credentials(credentials))
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
