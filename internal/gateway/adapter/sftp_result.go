package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// SFTPResult is encrypted for the mailbox owner. Its digest correlates the exact
// uploaded ciphertext, including refused inputs with no authenticated request ID.
type SFTPResult struct {
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	RequestSHA256 string                   `json:"request_sha256"`
	RequestID     string                   `json:"request_id,omitempty"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

// SFTPResponseScope binds the result to a fixed remote mailbox and native grant.
func SFTPResponseScope(realm, binding string, b config.SFTP) auth.ResponseScope {
	parts := []string{"airc-sftp-response-v1", b.ID, b.Address, b.Username, b.Root, strconv.FormatUint(uint64(b.RemoteUID), 10), b.Route.Target.Kind, b.Route.Target.Name}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return auth.ResponseScope{Realm: realm, Principal: b.Route.Principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func admitSFTP(parent context.Context, d *dispatch.Dispatcher, b config.SFTP, binding string, body []byte) SFTPResult {
	sum := sha256.Sum256(body)
	response := SFTPResult{Version: 1, Protocol: "sftp", RequestSHA256: hex.EncodeToString(sum[:]), Target: b.Route.Target}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	token := string(body)
	verified, err := d.AuthenticateDetached(token, true, time.Now())
	if err != nil {
		if slices.Contains(b.Route.Operations, core.Send) {
			// An expired intent can only recover an already retained matching
			// Send under current authority; it can never cause fresh execution.
			result, retainedBinding, retainedErr := d.RetainedEncryptedSend(ctx, token, b.Route.Principal, b.Route.Target, time.Now())
			if retainedErr == nil && retainedBinding == binding && result.Result != nil && result.Result.Durable {
				response.RequestID, response.Result = result.Result.RequestID, &result
				return response
			}
		}
		response.Error = &core.Failure{Code: core.AuthenticationFailed}
		return response
	}
	target, ok := verified.Target()
	if !ok || target != b.Route.Target || verified.PrincipalID() != b.Route.Principal || verified.Binding() != binding || !slices.Contains(b.Route.Operations, verified.Operation()) || !sftpOperation(verified.Operation()) {
		response.Error = &core.Failure{Code: core.Forbidden}
		return response
	}
	response.RequestID = verified.RequestID()
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err == nil && verified.Operation() == core.Send && (result.Result == nil || !result.Result.Durable) {
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

func sealSFTPResult(sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, realm, binding string, b config.SFTP, response SFTPResult) (string, error) {
	seal := func() (string, error) {
		payload, err := json.Marshal(response)
		if err != nil {
			return "", err
		}
		defer clear(payload)
		return sealer.Seal(SFTPResponseScope(realm, binding, b), b.Route.RecipientKeyID, recipient, payload, time.Now())
	}
	token, err := seal()
	if errors.Is(err, auth.ErrResponseCapacity) {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		return seal()
	}
	return token, err
}

func sftpOperation(operation core.Operation) bool {
	return operation == core.Send || operation == core.ContextRead || operation == core.HistoryRead || operation == core.Search
}
