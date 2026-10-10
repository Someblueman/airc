package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

const localREPMaxData = 65536

// LocalREPResponse is one whole REP reply. Only authenticated intent supplies ID.
// Local replies are clear; every request must be an encrypted signed envelope.
type LocalREPResponse struct {
	Version   int                      `json:"version"`
	RequestID string                   `json:"request_id,omitempty"`
	Result    *dispatch.DetachedResult `json:"result,omitempty"`
	Error     *core.Failure            `json:"error,omitempty"`
}

func localREPExchange(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, token string, encode func([]byte) ([]byte, error)) error {
	response := LocalREPResponse{Version: 1}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
		now := time.Now()
		verified, err := d.AuthenticateDetached(token, true, now)
		if err == nil {
			response.RequestID = verified.RequestID()
			switch verified.Operation() {
			case core.Send, core.ContextRead, core.HistoryRead, core.Search:
				ctx, cancel := context.WithTimeout(parent, 3*time.Second)
				result, dispatchErr := d.DetachedVerified(ctx, verified, time.Now())
				cancel()
				err = dispatchErr
				if err == nil {
					response.Result = &result
				}
			default:
				response.Error = &core.Failure{Code: core.Forbidden}
			}
		}
		if err != nil {
			status, code := httpError(err)
			response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
		}
	default:
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(body) > localREPMaxData {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		body, err = json.Marshal(response)
		if err != nil {
			return err
		}
	}
	wire, err := encode(body)
	if err != nil {
		return err
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if err = conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, err = io.Copy(conn, bytes.NewReader(wire))
	return err
}
