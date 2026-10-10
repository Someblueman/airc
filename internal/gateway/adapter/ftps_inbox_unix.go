//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
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

// FTPSInboxResult is encrypted for the fixed FTPS inbox recipient and
// correlated to the exact uploaded ciphertext, including refused inputs.
type FTPSInboxResult struct {
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	RequestSHA256 string                   `json:"request_sha256"`
	RequestID     string                   `json:"request_id,omitempty"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

// FTPSInboxResponseScope binds replies to the configured mailbox, target grant,
// principal binding and AIRC realm.
func FTPSInboxResponseScope(realm, binding string, l config.Listener) auth.ResponseScope {
	options := l.FTPSInbox
	parts := []string{
		"airc-ftps-inbox-response-v1", l.ID, l.Address,
		strconv.FormatUint(uint64(options.LoginUID), 10), options.Target.Kind, options.Target.Name,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return auth.ResponseScope{Realm: realm, Principal: options.Principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

// ProcessFTPSInbox handles one claimed upload and publishes exactly one
// encrypted result. Hash mismatches and cancellation leave the claim in place.
func ProcessFTPSInbox(ctx context.Context, mailbox *DirectoryMailbox, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, realm, binding string, l config.Listener) error {
	if ctx == nil || mailbox == nil || d == nil || sealer == nil || recipient == nil || realm == "" || binding == "" || l.FTPSInbox == nil {
		return store.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name, body, err := mailbox.Claim()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(body)
	if len(body) == 0 || len(body) > MaxRequestFrameBytes || name != ftpsInboxRequestName(body) {
		return store.ErrInvalid
	}
	response, err := admitFTPSInbox(ctx, d, l, binding, body)
	if err != nil {
		return err
	}
	payload, err := sealFTPSInboxResult(sealer, recipient, realm, binding, l, response)
	if err != nil {
		return err
	}
	defer clear(payload)
	if err := ctx.Err(); err != nil {
		return err
	}
	return mailbox.Publish(name, payload)
}

func admitFTPSInbox(parent context.Context, d *dispatch.Dispatcher, l config.Listener, binding string, body []byte) (FTPSInboxResult, error) {
	options := l.FTPSInbox
	sum := sha256.Sum256(body)
	response := FTPSInboxResult{
		Version: 1, Protocol: "ftps/inbox", RequestSHA256: hex.EncodeToString(sum[:]), Target: options.Target,
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	token := string(body)
	verified, err := d.AuthenticateDetached(token, true, time.Now())
	if err != nil {
		if slices.Contains(l.Operations, core.Send) {
			retained, retainedBinding, retainedErr := d.RetainedEncryptedSend(ctx, token, options.Principal, options.Target, time.Now())
			if errors.Is(retainedErr, store.ErrClosed) {
				return FTPSInboxResult{}, retainedErr
			}
			if retainedErr == nil && retainedBinding == binding && retained.Result != nil && retained.Result.Durable {
				response.RequestID, response.Result = retained.Result.RequestID, &retained
				return response, nil
			}
		}
		response.Error = &core.Failure{Code: core.AuthenticationFailed}
		return response, nil
	}
	target, ok := verified.Target()
	if !ok || target != options.Target || verified.PrincipalID() != options.Principal || verified.Binding() != binding || !slices.Contains(l.Operations, verified.Operation()) || !ftpsInboxOperation(verified.Operation()) {
		response.Error = &core.Failure{Code: core.Forbidden}
		return response, nil
	}
	response.RequestID = verified.RequestID()
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err == nil && verified.Operation() == core.Send && (result.Result == nil || !result.Result.Durable || result.Result.RequestID != verified.RequestID()) {
		err = store.ErrInvalid
	}
	if errors.Is(err, store.ErrClosed) || parent.Err() != nil {
		if err != nil {
			return FTPSInboxResult{}, err
		}
		return FTPSInboxResult{}, parent.Err()
	}
	if err != nil {
		status, code := httpError(err)
		response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		response.Result = &result
	}
	return response, nil
}

func ftpsInboxRequestName(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) + ".json"
}

func ftpsInboxOperation(operation core.Operation) bool {
	return operation == core.Send || operation == core.ContextRead || operation == core.HistoryRead || operation == core.Search
}

func sealFTPSInboxResult(sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, realm, binding string, l config.Listener, response FTPSInboxResult) ([]byte, error) {
	seal := func() (string, error) {
		payload, err := json.Marshal(response)
		if err != nil {
			return "", err
		}
		defer clear(payload)
		options := l.FTPSInbox
		return sealer.Seal(FTPSInboxResponseScope(realm, binding, l), options.RecipientKeyID, recipient, payload, time.Now())
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
