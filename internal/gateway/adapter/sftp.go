package adapter

import (
	"context"
	"errors"
	"path"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/sftpwire"
	"github.com/Someblueman/airc/internal/gateway/store"
	"golang.org/x/crypto/ssh"
)

// ServeSFTP owns one fixed single-request mailbox on an existing OpenSSH
// service: a plain contract request in request.json, answered by a plain
// result.json. Producers consume requests/results; this owner never deletes
// input. The SSH account and the mailbox's remote owner are the access gate;
// a request posts as the author it names, else the route's bound author.
func ServeSFTP(ctx context.Context, b config.SFTP, credentials *auth.SFTPCredentials, author dispatch.RouteAuthor, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	if ctx == nil || credentials == nil || d == nil || cap(slots) < 1 || cap(requests) < 1 {
		return store.ErrInvalid
	}
	b.Route.Operations = slices.Clone(b.Route.Operations)
	signer, err := ssh.NewSignerFromKey(credentials.PrivateKey)
	if err != nil {
		return err
	}
	backoff := time.Second
	for ctx.Err() == nil {
		pass, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := sftpPass(pass, b, signer, credentials.HostKey, author, d, slots, requests)
		cancel()
		if err == nil {
			backoff = time.Second
		} else {
			backoff = min(backoff*2, 15*time.Second)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func sftpPass(ctx context.Context, b config.SFTP, signer ssh.Signer, hostKey ssh.PublicKey, author dispatch.RouteAuthor, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	client, err := sftpwire.Dial(ctx, b.Address, b.Username, signer, hostKey)
	if err != nil {
		return err
	}
	defer client.Close()
	root, err := client.Lstat(b.Root)
	if err != nil || !root.Directory() || root.UID != b.RemoteUID || root.Permissions&07777 != 0700 {
		return auth.ErrAuthentication
	}
	resultPath := path.Join(b.Root, "result.json")
	if existing, err := client.Lstat(resultPath); err == nil {
		if !sftpFile(existing, b.RemoteUID, MaxSFTPResultBytes) {
			return auth.ErrAuthentication
		}
		return nil // Preserve the unread result, even across restart.
	} else if !errors.Is(err, sftpwire.ErrNotExist) {
		return err
	}
	temporary := path.Join(b.Root, "result.tmp")
	if _, err := client.Lstat(temporary); err == nil {
		return store.ErrConflict // Interrupted publication blocks new admission.
	} else if !errors.Is(err, sftpwire.ErrNotExist) {
		return err
	}
	requestPath := path.Join(b.Root, "request.json")
	info, err := client.Lstat(requestPath)
	if errors.Is(err, sftpwire.ErrNotExist) {
		return nil
	}
	if err != nil || !sftpFile(info, b.RemoteUID, MaxRequestFrameBytes) {
		return auth.ErrAuthentication
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return ctx.Err()
	}
	body, opened, err := client.ReadFile(requestPath, MaxRequestFrameBytes)
	defer clear(body)
	if err != nil || !sftpFile(opened, b.RemoteUID, MaxRequestFrameBytes) || opened.Size != uint64(len(body)) {
		return auth.ErrAuthentication
	}
	response := admitSFTP(ctx, d, author, b, body)
	token, err := encodeSFTPResult(response)
	if err != nil || ctx.Err() != nil {
		if err != nil {
			return err
		}
		return ctx.Err()
	}
	// An interrupted or concurrent publisher may own result.tmp. Never remove
	// it or infer ownership from its age; exclusive creation fails closed until
	// the owner completes or the operator recovers the stopped mailbox.
	written, err := client.WriteExclusive(temporary, token)
	if err != nil {
		return err
	}
	if !sftpFile(written, b.RemoteUID, MaxSFTPResultBytes) || written.Size != uint64(len(token)) {
		return auth.ErrAuthentication
	}
	return client.Rename(temporary, resultPath)
}

func sftpFile(info sftpwire.Attrs, uid uint32, maximum int) bool {
	return info.Regular() && info.UID == uid && info.Permissions&07777 == 0600 && info.Size > 0 && info.Size <= uint64(maximum)
}
