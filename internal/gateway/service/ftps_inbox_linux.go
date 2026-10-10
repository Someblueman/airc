//go:build linux

package service

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

type ftpsInboxOwner struct {
	mailbox    *adapter.DirectoryMailbox
	options    config.Listener
	realm      string
	binding    string
	dispatcher *dispatch.Dispatcher
	sealer     *auth.ResponseSealer
	recipient  *ecdsa.PublicKey
	requests   chan struct{}
}

// openFTPSInbox prepares a private filesystem bridge to the separately
// provisioned vsftpd account. It does not start or configure the FTP service.
func openFTPSInbox(c config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (mailboxOwner, error) {
	if l.FTPSInbox == nil || os.Geteuid() <= 0 || uint64(os.Geteuid()) != uint64(l.FTPSInbox.LoginUID) {
		return nil, errors.New("FTPS inbox login UID does not match the gateway owner")
	}
	options := *l.FTPSInbox
	l.FTPSInbox = &options
	l.Operations = slices.Clone(l.Operations)
	o := &ftpsInboxOwner{options: l, realm: c.Realm, dispatcher: d, requests: requests}
	for _, principal := range c.Principals {
		if principal.ID == options.Principal && !principal.Revoked {
			o.binding = principal.Binding
			break
		}
	}
	if o.binding == "" {
		return nil, auth.ErrCredential
	}
	var err error
	o.recipient, err = auth.ReadResponseRecipientKey(options.RecipientPublicKeyFile)
	if err != nil {
		return nil, err
	}
	o.sealer, err = loadSignedResponseSigner(c)
	if err != nil {
		return nil, err
	}
	o.mailbox, err = adapter.OpenDirectoryMailbox(l.Address)
	if err != nil {
		o.sealer.Close()
		return nil, err
	}
	return o, nil
}

func (o *ftpsInboxOwner) Close() error {
	o.sealer.Close()
	return o.mailbox.Close()
}

func (o *ftpsInboxOwner) serve(ctx context.Context) error {
	for ctx.Err() == nil {
		select {
		case o.requests <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		pass, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := adapter.ProcessFTPSInbox(pass, o.mailbox, o.dispatcher, o.sealer, o.recipient, o.realm, o.binding, o.options)
		cancel()
		<-o.requests
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, adapter.ErrMailboxResultPending) {
			return err
		}
		// Bound idle polling and avoid starving other configured ingress owners.
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
