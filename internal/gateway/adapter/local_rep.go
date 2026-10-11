package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

const (
	localREPMaxData = 65536
	// localREPMessageDeadline bounds reading one message once its first byte
	// has arrived, and writing one reply. An idle connection between
	// messages has no deadline: like an IRC client, a REQ socket may stay
	// connected, bounded only by the listener's connection caps.
	localREPMessageDeadline = 3 * time.Second
)

// LocalREPResponse is one whole REP reply: the request's ID and either its
// DetachedResult or a failure, as plain contract JSON.
type LocalREPResponse struct {
	Version   int                      `json:"version"`
	RequestID string                   `json:"request_id,omitempty"`
	Result    *dispatch.DetachedResult `json:"result,omitempty"`
	Error     *core.Failure            `json:"error,omitempty"`
}

// awaitLocalREPMessage waits without a deadline for the first byte of the next
// message, then starts that message's deadline. Closing the connection (on
// shutdown) ends the wait.
func awaitLocalREPMessage(conn net.Conn, r *bufio.Reader) error {
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	if _, err := r.Peek(1); err != nil {
		return err
	}
	return conn.SetReadDeadline(time.Now().Add(localREPMessageDeadline))
}

func writeLocalREP(parent context.Context, conn net.Conn, wire []byte) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if err := conn.SetWriteDeadline(time.Now().Add(localREPMessageDeadline)); err != nil {
		return err
	}
	_, err := io.Copy(conn, bytes.NewReader(wire))
	return err
}

// localREPExchange answers one request. A body that is a JSON object is a
// plain contract request run as its author (or the connection's identified
// author); any other body is the legacy encrypted signed envelope.
func localREPExchange(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, body []byte, encode func([]byte) ([]byte, error), plain *dispatch.Ingress) error {
	response := LocalREPResponse{Version: 1}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
		ctx, cancel := context.WithTimeout(parent, localREPMessageDeadline)
		if len(body) > 0 && body[0] == '{' {
			localREPPlain(ctx, d, plain, body, &response)
		} else {
			localREPLegacy(ctx, d, string(body), &response)
		}
		cancel()
	default:
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
	}
	reply, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(reply) > localREPMaxData {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		if reply, err = json.Marshal(response); err != nil {
			return err
		}
	}
	wire, err := encode(reply)
	if err != nil {
		return err
	}
	return writeLocalREP(parent, conn, wire)
}

func localREPPlain(ctx context.Context, d *dispatch.Dispatcher, plain *dispatch.Ingress, body []byte, response *LocalREPResponse) {
	if plain == nil {
		response.Error = &core.Failure{Code: core.UnsupportedOperation}
		return
	}
	sub, err := core.DecodeSubmission(body)
	if err != nil {
		response.Error = &core.Failure{Code: core.InvalidRequest}
		return
	}
	response.RequestID = sub.Request.RequestID
	result, err := d.SubmitDecoded(ctx, *plain, sub, time.Now())
	if err != nil {
		_, failure := errorFailure(err)
		response.Error = &failure
		return
	}
	response.Result = &result
}

// localREPLegacy is the temporary principal shim: an encrypted signed
// envelope limited to send, context, history and search.
func localREPLegacy(ctx context.Context, d *dispatch.Dispatcher, token string, response *LocalREPResponse) {
	verified, err := d.AuthenticateDetached(token, true, time.Now())
	if err == nil {
		response.RequestID = verified.RequestID()
		switch verified.Operation() {
		case core.Send, core.ContextRead, core.HistoryRead, core.Search:
			var result dispatch.DetachedResult
			result, err = d.DetachedVerified(ctx, verified, time.Now())
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
}
