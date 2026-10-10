package adapter

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    *core.Failure `json:"data,omitempty"`
}

func rpcFailure(id json.RawMessage, code int, message string, err error) rpcResponse {
	if message == "" {
		message = "Application error"
	}
	var data *core.Failure
	if err != nil {
		_, failure := failureForRPC(err)
		data = &failure
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}}
}

func failureForRPC(err error) (int, core.Failure) {
	_, code := httpError(err)
	return -32000, core.Failure{Code: code, Transient: errors.Is(err, store.ErrCapacity) || errors.Is(err, backend.ErrReadUnavailable)}
}

func rpcCodeMessage(code int) string {
	switch code {
	case -32700:
		return "Parse error"
	case -32600:
		return "Invalid Request"
	case -32601:
		return "Method not found"
	case -32602:
		return "Invalid params"
	default:
		return "Application error"
	}
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string, data *core.Failure) {
	writeRPCResponse(w, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}

func writeRPCResponse(w http.ResponseWriter, response rpcResponse) {
	data, err := json.Marshal(response)
	if err != nil || len(data)+1 > maxRPCResponseBytes {
		fallback, _ := json.Marshal(rpcFailure(response.ID, -32000, "Application error", store.ErrCapacity))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append(fallback, '\n'))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(data, '\n'))
}

func writeRPCBatch(w http.ResponseWriter, responses []rpcResponse) {
	const errorReserve = 2048 // Includes a maximally HTML-escaped bounded ID.
	data := make([]byte, 0, 4096)
	data = append(data, '[')
	for i, response := range responses {
		if i > 0 {
			data = append(data, ',')
		}
		encoded, err := json.Marshal(response)
		remaining := (len(responses) - i - 1) * (errorReserve + 1)
		if err != nil || len(data)+len(encoded)+remaining+2 > maxRPCResponseBytes {
			encoded, _ = json.Marshal(rpcFailure(response.ID, -32000, rpcCodeMessage(-32000), store.ErrCapacity))
		}
		data = append(data, encoded...)
	}
	data = append(data, ']', '\n')
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
