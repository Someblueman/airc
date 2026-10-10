package adapter

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/sqlitemailbox"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeSQLiteMailbox serially processes the database's single request slot. The
// database package owns locking, schema validation and atomic result persistence.
func ServeSQLiteMailbox(ctx context.Context, b config.SQLiteMailbox, mailbox *sqlitemailbox.Mailbox, realm, binding string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, requests chan struct{}) error {
	if ctx == nil || mailbox == nil || d == nil || sealer == nil || recipient == nil || realm == "" || binding == "" || cap(requests) < 1 {
		return store.ErrInvalid
	}
	b.Route.Operations = slices.Clone(b.Route.Operations)
	backoff := time.Second
	for ctx.Err() == nil {
		pass, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := processSQLiteMailbox(pass, mailbox, b, realm, binding, d, sealer, recipient, requests)
		cancel()
		if errors.Is(err, sqlitemailbox.ErrClosed) {
			return err
		}
		delay := time.Second
		if err == nil {
			backoff = time.Second
		} else {
			delay = backoff
			backoff = min(backoff*2, 15*time.Second)
		}
		if !sqliteMailboxWait(ctx, delay) {
			return nil
		}
	}
	return nil
}

func processSQLiteMailbox(ctx context.Context, mailbox *sqlitemailbox.Mailbox, b config.SQLiteMailbox, realm, binding string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, requests chan struct{}) error {
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return mailbox.Process(ctx, func(request []byte) ([]byte, error) {
		if len(request) == 0 || len(request) > MaxRequestFrameBytes {
			return nil, store.ErrInvalid
		}
		response := admitSQLiteMailbox(ctx, d, b, binding, request)
		return sealSQLiteMailboxResult(sealer, recipient, realm, binding, b, response)
	})
}

func sqliteMailboxWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
