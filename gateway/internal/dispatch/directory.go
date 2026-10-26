package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func (d *Dispatcher) directory(ctx context.Context, who actor, request core.Request, now time.Time) (backend.DirectorySnapshot, error) {
	if !d.enabled(who, core.Directory) {
		return backend.DirectorySnapshot{}, ErrUnsupported
	}
	grant, err := d.authorizeRead(ctx, who, request, now)
	if err != nil {
		return backend.DirectorySnapshot{}, err
	}
	return d.backend.Directory(ctx, grant)
}
