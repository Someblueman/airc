package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/jsoninput"
)

const (
	maxRPCSingleBytes   = core.MaxRequestBytes + 1024
	maxRPCBatchBytes    = 64 << 10
	maxRPCBatchItems    = 16
	maxRPCResponseBytes = 1 << 20
)

type rpcMessage struct {
	ID        json.RawMessage
	HasID     bool
	Method    string
	Params    json.RawMessage
	HasParams bool
}

// rpcCaller performs JSON-RPC's contract requests through the listener's
// author entry point.
type rpcCaller struct {
	call         func(ctx context.Context, operation core.Operation, params []byte) (any, error)
	capabilities func(ctx context.Context) (any, error)
	watch        func(ctx context.Context, params []byte, emit func(backend.HistoryPage) error) error
}

// authorRPC answers airc.request with the same DetachedResult every carrier
// returns for a plain request.
func authorRPC(d *dispatch.Dispatcher, in dispatch.Ingress) rpcCaller {
	return rpcCaller{
		call: func(ctx context.Context, _ core.Operation, params []byte) (any, error) {
			return d.Submit(ctx, in, params, time.Now())
		},
		capabilities: func(ctx context.Context) (any, error) {
			result, err := d.SubmitDecoded(ctx, in, core.Submission{Request: core.Request{Version: 1, RequestID: "capabilities", Operation: core.CapabilitiesGet}}, time.Now())
			if err != nil {
				return nil, err
			}
			return result.Capabilities, nil
		},
		watch: func(ctx context.Context, params []byte, emit func(backend.HistoryPage) error) error {
			return d.SubmitWatch(ctx, in, params, time.Now(), emit)
		},
	}
}

func httpJSONRPC(w http.ResponseWriter, r *http.Request, caller rpcCaller) {
	if !validRPCHeaders(r, false) {
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRPCBatchBytes))
	if err != nil {
		code := rpcBodyErrorCode(err)
		writeRPCError(w, nil, code, rpcCodeMessage(code), nil)
		return
	}
	responses, batch, parseCode := processRPCBody(ctx, caller, body)
	if parseCode != 0 {
		writeRPCError(w, nil, parseCode, rpcCodeMessage(parseCode), nil)
		return
	}
	if len(responses) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if batch {
		writeRPCBatch(w, responses)
		return
	}
	writeRPCResponse(w, responses[0])
}

func rpcBodyErrorCode(err error) int {
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		return -32600
	}
	return -32700
}

func processRPCBody(ctx context.Context, caller rpcCaller, body []byte) ([]rpcResponse, bool, int) {
	if len(body) == 0 || !utf8.Valid(body) {
		return nil, false, -32700
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, false, -32700
	}
	if len(trimmed) > maxRPCBatchBytes {
		return nil, false, -32600
	}
	if len(trimmed) > maxRPCSingleBytes && trimmed[0] != '[' {
		return nil, false, -32600
	}
	if trimmed[0] == '[' {
		var raw []json.RawMessage
		if !json.Valid(trimmed) {
			return nil, true, -32700
		}
		if err := jsoninput.Validate(trimmed, maxRPCBatchBytes); err != nil || json.Unmarshal(trimmed, &raw) != nil || len(raw) == 0 || len(raw) > maxRPCBatchItems {
			return nil, true, -32600
		}
		responses := make([]rpcResponse, 0, len(raw))
		for _, item := range raw {
			response, ok := processRPCItem(ctx, caller, item)
			if ok {
				responses = append(responses, response)
			}
		}
		return responses, true, 0
	}
	response, ok := processRPCItem(ctx, caller, trimmed)
	if !ok {
		return nil, false, 0
	}
	return []rpcResponse{response}, false, 0
}

func processRPCItem(ctx context.Context, caller rpcCaller, body []byte) (rpcResponse, bool) {
	req, code := decodeRPCMessage(body)
	if code != 0 {
		if !req.HasID && code == -32602 {
			return rpcResponse{}, false
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: rpcCodeMessage(code)}}, true
	}
	// A notification may only post (send or reply, checked below); others
	// get no answer.
	if !req.HasID && req.Method != "airc.request" {
		return rpcResponse{}, false
	}
	if req.Method == "airc.request" {
		submission, err := core.DecodeSubmission(req.Params)
		if err != nil {
			if !req.HasID {
				return rpcResponse{}, false
			}
			return rpcFailure(req.ID, -32602, rpcCodeMessage(-32602), nil), true
		}
		request := submission.Request
		if !req.HasID && request.Operation != core.Send && request.Operation != core.Reply {
			return rpcResponse{}, false
		}
		value, err := caller.call(ctx, request.Operation, req.Params)
		if err != nil {
			if !req.HasID {
				return rpcResponse{}, false
			}
			return rpcFailure(req.ID, -32000, "", err), true
		}
		if !req.HasID {
			return rpcResponse{}, false
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: value}, true
	}
	if req.Method == "airc.capabilities" {
		// decodeRPCMessage already refused non-empty params.
		value, err := caller.capabilities(ctx)
		if err != nil {
			return rpcFailure(req.ID, -32000, "", err), true
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: value}, true
	}
	return rpcFailure(req.ID, -32601, rpcCodeMessage(-32601), nil), req.HasID
}

func decodeRPCMessage(body []byte) (rpcMessage, int) {
	if len(body) == 0 || !utf8.Valid(body) {
		return rpcMessage{}, -32700
	}
	if len(body) > maxRPCSingleBytes {
		return rpcMessage{}, -32600
	}
	if !json.Valid(body) {
		return rpcMessage{}, -32700
	}
	if jsoninput.Validate(body, maxRPCSingleBytes) != nil {
		return rpcMessage{}, -32600
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return rpcMessage{}, -32600
	}
	for name := range fields {
		if name != "jsonrpc" && name != "id" && name != "method" && name != "params" {
			return rpcMessage{}, -32600
		}
	}
	var version string
	if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" {
		return rpcMessage{}, -32600
	}
	var method string
	methodJSON := bytes.TrimSpace(fields["method"])
	if len(methodJSON) == 0 || methodJSON[0] != '"' || json.Unmarshal(methodJSON, &method) != nil {
		return rpcMessage{}, -32600
	}
	result := rpcMessage{Method: method}
	if raw, ok := fields["id"]; ok {
		if len(raw) > 256 || !rpcID(raw) {
			return rpcMessage{}, -32600
		}
		result.ID, result.HasID = append([]byte(nil), raw...), true
	}
	if raw, ok := fields["params"]; ok {
		trimmed := bytes.TrimSpace(raw)
		if string(trimmed) == "null" || len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
			return rpcMessage{}, -32600
		}
		result.Params, result.HasParams = append([]byte(nil), raw...), true
	}
	if method == "airc.request" && !result.HasParams {
		return result, -32602
	}
	if method == "airc.request" && bytes.TrimSpace(result.Params)[0] != '{' {
		return result, -32602
	}
	if method == "airc.capabilities" && result.HasParams && !emptyRPCObject(result.Params) {
		return result, -32602
	}
	return result, 0
}

func emptyRPCObject(raw []byte) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && fields != nil && len(fields) == 0
}

func rpcID(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return true
	}
	if len(raw) == 0 || raw[0] == '"' {
		var value string
		return json.Unmarshal(raw, &value) == nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return false
	}
	_, ok := value.(json.Number)
	return ok
}

func validRPCHeaders(r *http.Request, stream bool) bool {
	if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Last-Event-ID")) != 0 || len(r.Header.Values("Idempotency-Key")) != 0 {
		return false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return false
	}
	if stream {
		accept := r.Header.Values("Accept")
		if len(accept) != 1 {
			return false
		}
		for _, part := range strings.Split(accept[0], ",") {
			media, params, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err == nil && media == "text/event-stream" {
				if quality, present := params["q"]; present {
					q, err := strconv.ParseFloat(quality, 64)
					if err != nil || !(q > 0 && q <= 1) {
						continue
					}
				}
				return true
			}
		}
		return false
	}
	return len(r.Header.Values("Accept")) <= 1
}
