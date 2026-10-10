package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) Directory(ctx context.Context, token string, body []byte, now time.Time) (backend.DirectorySnapshot, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return backend.DirectorySnapshot{}, err
	}
	request, err := core.Decode(body)
	if err != nil || request.Operation != core.Directory {
		return backend.DirectorySnapshot{}, store.ErrInvalid
	}
	return d.directory(ctx, identity, request, now)
}

func (d *Dispatcher) directory(ctx context.Context, identity auth.Identity, request core.Request, now time.Time) (backend.DirectorySnapshot, error) {
	if !slices.Contains(d.operations, core.Directory) {
		return backend.DirectorySnapshot{}, ErrUnsupported
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, request, now)
	if err != nil {
		return backend.DirectorySnapshot{}, err
	}
	return d.backend.Directory(ctx, grant)
}
