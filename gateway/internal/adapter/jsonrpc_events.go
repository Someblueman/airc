package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
)

func httpJSONRPCEvents(w http.ResponseWriter, r *http.Request, caller rpcCaller) {
	if !validRPCHeaders(r, true) {
		writeRPCError(w, nil, -32600, "Invalid Request", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	control := http.NewResponseController(w)
	defer func() { _ = control.SetWriteDeadline(time.Now().Add(5 * time.Second)) }()
	if err := control.SetWriteDeadline(time.Now().Add(23 * time.Second)); err != nil {
		httpFailure(w, http.StatusServiceUnavailable, core.UpstreamUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRPCSingleBytes))
	if err != nil {
		code := rpcBodyErrorCode(err)
		writeRPCError(w, nil, code, rpcCodeMessage(code), nil)
		return
	}
	trimmedBody := bytes.TrimSpace(body)
	req, code := decodeRPCMessage(trimmedBody)
	if len(trimmedBody) == 0 {
		code = -32700
	}
	if code != 0 || req.Method != "airc.request" || !req.HasID {
		if !req.HasID && code == -32602 || code == 0 && !req.HasID {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if code == 0 {
			code = -32601
		}
		id := req.ID
		if code == -32600 || code == -32700 {
			id = nil
		}
		writeRPCError(w, id, code, rpcCodeMessage(code), nil)
		return
	}
	submission, err := core.DecodeSubmission(req.Params)
	if err != nil || !submission.Request.Operation.IsWatch() {
		writeRPCError(w, req.ID, -32602, "Invalid params", nil)
		return
	}
	started := false
	bytesWritten, frames := 0, 0
	const terminalReserve = backend.MaxHistoryBytes
	var last backend.HistoryPage
	write := func(value any, terminal bool) error {
		data, err := json.Marshal(value)
		frame := append([]byte("event: message\ndata: "), data...)
		frame = append(frame, '\n', '\n')
		if err != nil || len(frame) > backend.MaxHistoryBytes || (terminal && bytesWritten+len(frame) > MaxStreamBytes) || (!terminal && (bytesWritten+len(frame)+terminalReserve > MaxStreamBytes || frames >= MaxStreamFrames)) {
			return store.ErrCapacity
		}
		if err := control.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return err
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
		if err := control.Flush(); err != nil {
			return err
		}
		started = true
		bytesWritten += len(frame)
		if !terminal {
			frames++
		}
		return nil
	}
	err = caller.watch(ctx, req.Params, func(page backend.HistoryPage) error {
		err := write(struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  any    `json:"params"`
		}{"2.0", "airc.page", struct {
			ID   json.RawMessage     `json:"id"`
			Page backend.HistoryPage `json:"page"`
		}{req.ID, page}}, false)
		if err == nil {
			last = page
		}
		return err
	})
	if r.Context().Err() != nil {
		return
	}
	reason := watchEndReason(err)
	var terminal any
	if err != nil && !started {
		terminal = rpcFailure(req.ID, -32000, "", err)
	} else {
		terminal = rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"page": last, "reason": reason}}
	}
	if terminalErr := write(terminal, true); errors.Is(terminalErr, store.ErrCapacity) {
		// A full terminal page can exceed the remaining stream budget. Emit a
		// compact typed failure with the original correlation id when possible.
		_ = write(rpcFailure(req.ID, -32000, "Application error", store.ErrCapacity), true)
	}
}
