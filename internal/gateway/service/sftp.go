package service

import (
	"crypto/ecdsa"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type sftpOwner struct {
	options     config.SFTP
	binding     string
	credentials auth.SFTPCredentials
	dispatcher  *dispatch.Dispatcher
	sealer      *auth.ResponseSealer
	recipient   *ecdsa.PublicKey
}

func (o *sftpOwner) close() {
	o.credentials.Close()
	o.sealer.Close()
}

// prepareSFTP loads only local provisioning. It neither contacts nor configures
// an SSH server. Run joins mailbox owners before clearing their credentials.
func prepareSFTP(c config.Config, s *store.Store, v *auth.Verifier, b *backend.Backend, cursors *auth.CursorCodec) (owners []*sftpOwner, err error) {
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
		for _, principal := range c.Principals {
			if principal.ID == options.Route.Principal && !principal.Revoked {
				o.binding = principal.Binding
				break
			}
		}
		if o.binding == "" {
			return owners, auth.ErrCredential
		}
		o.credentials, err = auth.ReadSFTPCredentials(options.PrivateKeyFile, options.HostKeyFile)
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
