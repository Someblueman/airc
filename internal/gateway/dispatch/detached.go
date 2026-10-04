package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// DetachedResult has exactly one operation-specific result on success.
type DetachedResult struct {
	Version      int                        `json:"version"`
	Operation    core.Operation             `json:"operation"`
	Result       *core.Result               `json:"result,omitempty"`
	Page         *backend.HistoryPage       `json:"page,omitempty"`
	Directory    *backend.DirectorySnapshot `json:"directory,omitempty"`
	Capabilities *Capabilities              `json:"capabilities,omitempty"`
}

func (d *Dispatcher) Detached(ctx context.Context, token string, encrypted bool, now time.Time) (DetachedResult, error) {
	message, err := d.authenticateDetached(token, encrypted, now)
	if err != nil {
		return DetachedResult{}, err
	}
	result := DetachedResult{Version: 1, Operation: message.Request.Operation}
	if message.Request.Operation == core.CapabilitiesGet {
		readCtx, cancel := context.WithDeadline(ctx, time.Unix(message.ReplayUntil, 0))
		defer cancel()
		capabilities, err := d.capabilities(readCtx, message.Identity)
		if err != nil {
			return DetachedResult{}, err
		}
		result.Capabilities = &capabilities
		return result, nil
	}
	switch message.Request.Operation {
	case core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.Directory:
		// Reads cannot extend the signed validity-plus-skew boundary. A detached
		// read remains a one-shot lease; renewal must authenticate a new envelope.
		readCtx, cancel := context.WithDeadline(ctx, time.Unix(message.ReplayUntil, 0))
		defer cancel()
		if message.Request.Operation == core.ReceiptGet {
			delivery, e := d.execute(readCtx, message.Identity, message.Request, message.ReplayUntil, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Result = &delivery
		} else if message.Request.Operation == core.Directory {
			snapshot, e := d.directory(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Directory = &snapshot
		} else {
			page, e := d.readPage(readCtx, message.Identity, message.Request, "", now, nil)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Page = &page
		}
	default:
		delivery, e := d.execute(ctx, message.Identity, message.Request, message.ReplayUntil, now)
		if e != nil {
			return DetachedResult{}, e
		}
		result.Result = &delivery
	}
	return result, nil
}

func (d *Dispatcher) authenticateDetached(token string, encrypted bool, now time.Time) (auth.AuthenticatedMessage, error) {
	if encrypted {
		return d.verifier.AuthenticateEncrypted(token, now)
	}
	return d.verifier.AuthenticateSigned(token, now)
}
