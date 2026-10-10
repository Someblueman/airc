package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// SQLiteMailboxResult is sealed for the configured mailbox reader and tied to
// the exact request ciphertext, including requests refused before authentication.
type SQLiteMailboxResult struct {
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	RequestSHA256 string                   `json:"request_sha256"`
	RequestID     string                   `json:"request_id,omitempty"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

// SQLiteMailboxResponseScope binds responses to this fixed database mailbox,
// target grant, principal binding and AIRC realm.
func SQLiteMailboxResponseScope(realm, binding string, b config.SQLiteMailbox) auth.ResponseScope {
	parts := []string{"airc-sqlite-mailbox-response-v1", b.ID, b.Directory, b.Route.Target.Kind, b.Route.Target.Name}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return auth.ResponseScope{Realm: realm, Principal: b.Route.Principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func admitSQLiteMailbox(parent context.Context, d *dispatch.Dispatcher, b config.SQLiteMailbox, binding string, body []byte) SQLiteMailboxResult {
	sum := sha256.Sum256(body)
	response := SQLiteMailboxResult{Version: 1, Protocol: "sqlite/mailbox", RequestSHA256: hex.EncodeToString(sum[:]), Target: b.Route.Target}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	token := string(body)
	verified, err := d.AuthenticateDetached(token, true, time.Now())
	if err != nil {
		if slices.Contains(b.Route.Operations, core.Send) {
			retained, retainedBinding, retainedErr := d.RetainedEncryptedSend(ctx, token, b.Route.Principal, b.Route.Target, time.Now())
			if retainedErr == nil && retainedBinding == binding && retained.Result != nil && retained.Result.Durable {
				response.RequestID, response.Result = retained.Result.RequestID, &retained
				return response
			}
		}
		response.Error = &core.Failure{Code: core.AuthenticationFailed}
		return response
	}
	target, ok := verified.Target()
	if !ok || target != b.Route.Target || verified.PrincipalID() != b.Route.Principal || verified.Binding() != binding || !slices.Contains(b.Route.Operations, verified.Operation()) || !sqliteMailboxOperation(verified.Operation()) {
		response.Error = &core.Failure{Code: core.Forbidden}
		return response
	}
	response.RequestID = verified.RequestID()
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err == nil && verified.Operation() == core.Send && (result.Result == nil || !result.Result.Durable || result.Result.RequestID != verified.RequestID()) {
		err = store.ErrInvalid
	}
	if err != nil {
		status, code := httpError(err)
		response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		response.Result = &result
	}
	return response
}

func sqliteMailboxOperation(operation core.Operation) bool {
	return operation == core.Send || operation == core.ContextRead || operation == core.HistoryRead || operation == core.Search
}

func sealSQLiteMailboxResult(sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, realm, binding string, b config.SQLiteMailbox, response SQLiteMailboxResult) ([]byte, error) {
	seal := func() (string, error) {
		payload, err := json.Marshal(response)
		if err != nil {
			return "", err
		}
		defer clear(payload)
		return sealer.Seal(SQLiteMailboxResponseScope(realm, binding, b), b.Route.RecipientKeyID, recipient, payload, time.Now())
	}
	token, err := seal()
	if errors.Is(err, auth.ErrResponseCapacity) {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		token, err = seal()
	}
	if err != nil {
		return nil, err
	}
	if len(token) == 0 || len(token) > auth.MaxResponseTokenBytes {
		return nil, auth.ErrResponseCapacity
	}
	return []byte(token), nil
}
