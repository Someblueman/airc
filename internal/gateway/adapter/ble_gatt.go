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

// BLEGATTResult is sealed as a whole before being paged over the radio. The
// transfer ID is transport correlation; RequestID is the native durable ID.
type BLEGATTResult struct {
	Version       int                      `json:"version"`
	Protocol      string                   `json:"protocol"`
	TransferID    string                   `json:"transfer_id"`
	RequestSHA256 string                   `json:"request_sha256"`
	RequestID     string                   `json:"request_id,omitempty"`
	Target        core.Target              `json:"target"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

func BLEGATTResponseScope(realm, binding string, l config.Listener, peer config.BLEGATTPeer) auth.ResponseScope {
	parts := []string{"airc-ble-gatt-response-v1", l.ID, l.Address, l.BLEGATT.AdapterAddress,
		peer.Address, peer.AddressType, peer.Target.Kind, peer.Target.Name}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return auth.ResponseScope{Realm: realm, Principal: peer.Principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

// ProcessBLEGATT verifies native author authority independently of BlueZ peer
// admission, and returns only a whole encrypted response for the fixed peer.
func ProcessBLEGATT(parent context.Context, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, realm, binding string, l config.Listener, peer config.BLEGATTPeer, transfer [8]byte, body []byte) ([]byte, error) {
	if parent == nil || d == nil || sealer == nil || recipient == nil || realm == "" || binding == "" || l.BLEGATT == nil || transfer == [8]byte{} || len(body) < 1 || len(body) > MaxRequestFrameBytes {
		return nil, store.ErrInvalid
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	response := BLEGATTResult{Version: 1, Protocol: "ble/gatt", TransferID: hex.EncodeToString(transfer[:]), RequestSHA256: hex.EncodeToString(sum[:]), Target: peer.Target}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	token := string(body)
	verified, err := d.AuthenticateDetached(token, true, time.Now())
	if err != nil {
		if slices.Contains(l.Operations, core.Send) {
			retained, retainedBinding, retainedErr := d.RetainedEncryptedSend(ctx, token, peer.Principal, peer.Target, time.Now())
			if errors.Is(retainedErr, store.ErrClosed) {
				return nil, retainedErr
			}
			if retainedErr == nil && retainedBinding == binding && retained.Result != nil && retained.Result.Durable {
				response.RequestID, response.Result = retained.Result.RequestID, &retained
			}
		}
		if response.Result == nil {
			response.Error = &core.Failure{Code: core.AuthenticationFailed}
		}
	} else {
		target, ok := verified.Target()
		operation := verified.Operation()
		allowed := operation == core.Send || operation == core.ContextRead || operation == core.HistoryRead || operation == core.Search
		if !ok || target != peer.Target || verified.PrincipalID() != peer.Principal || verified.Binding() != binding || !slices.Contains(l.Operations, operation) || !allowed {
			response.Error = &core.Failure{Code: core.Forbidden}
		} else {
			response.RequestID = verified.RequestID()
			result, dispatchErr := d.DetachedVerified(ctx, verified, time.Now())
			if dispatchErr == nil && operation == core.Send && (result.Result == nil || !result.Result.Durable || result.Result.RequestID != verified.RequestID()) {
				dispatchErr = store.ErrInvalid
			}
			if errors.Is(dispatchErr, store.ErrClosed) {
				return nil, dispatchErr
			}
			if dispatchErr != nil {
				status, code := httpError(dispatchErr)
				response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
			} else {
				response.Result = &result
			}
		}
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	seal := func() (string, error) {
		payload, err := json.Marshal(response)
		if err != nil {
			return "", err
		}
		defer clear(payload)
		return sealer.Seal(BLEGATTResponseScope(realm, binding, l, peer), peer.RecipientKeyID, recipient, payload, time.Now())
	}
	protected, err := seal()
	if errors.Is(err, auth.ErrResponseCapacity) {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		protected, err = seal()
	}
	if err != nil {
		return nil, err
	}
	if len(protected) == 0 || len(protected) > auth.MaxResponseTokenBytes {
		return nil, auth.ErrResponseCapacity
	}
	return []byte(protected), nil
}
