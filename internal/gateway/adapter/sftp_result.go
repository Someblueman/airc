package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// MaxSFTPResultBytes bounds a mailbox result.json.
const MaxSFTPResultBytes = 64 << 10

// SFTPResult is the plain result.json. Its digest correlates the exact
// uploaded request, including refused inputs with no request ID.
type SFTPResult struct {
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	RequestSHA256 string                   `json:"request_sha256"`
	RequestID     string                   `json:"request_id,omitempty"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

func admitSFTP(parent context.Context, d *dispatch.Dispatcher, author dispatch.RouteAuthor, b config.SFTP, body []byte) SFTPResult {
	sum := sha256.Sum256(body)
	response := SFTPResult{Version: 1, Protocol: "sftp", RequestSHA256: hex.EncodeToString(sum[:]), Target: b.Route.Target}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	sub, err := core.DecodeSubmission(body)
	if err != nil {
		response.Error = &core.Failure{Code: core.InvalidRequest}
		return response
	}
	response.RequestID = sub.Request.RequestID
	op := sub.Request.Operation
	if !routeTargetMatches(sub.Request, b.Route.Target) || !slices.Contains(b.Route.Operations, op) || !sftpOperation(op) {
		response.Error = &core.Failure{Code: core.Forbidden}
		return response
	}
	result, err := d.SubmitRouteDecoded(ctx, author, dispatch.RouteSource(b.ID), sub, time.Now())
	if err == nil && op == core.Send && (result.Result == nil || !result.Result.Durable) {
		err = store.ErrInvalid
	}
	if err != nil {
		failure := errorFailureOf(err)
		response.Error = &failure
	} else {
		response.Result = &result
	}
	return response
}

// encodeSFTPResult replaces a result too large for the mailbox with a
// transient capacity failure, never a truncated one.
func encodeSFTPResult(response SFTPResult) ([]byte, error) {
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxSFTPResultBytes {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		if payload, err = json.Marshal(response); err != nil || len(payload) > MaxSFTPResultBytes {
			return nil, errors.Join(errResultCapacity, err)
		}
	}
	return payload, nil
}

func sftpOperation(operation core.Operation) bool {
	return operation == core.Send || operation == core.ContextRead || operation == core.HistoryRead || operation == core.Search
}
