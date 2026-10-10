package adapter

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestJSONRPCBatchCapacityKeepsEveryCorrelationAndLaterAdmission(t *testing.T) {
	responses := make([]rpcResponse, maxRPCBatchItems)
	for i := range responses {
		id, _ := json.Marshal(strings.Repeat("<", 245) + fmt.Sprint(i))
		responses[i] = rpcResponse{JSONRPC: "2.0", ID: id, Result: strings.Repeat("x", 80<<10)}
	}
	responses[len(responses)-1].Result = core.Result{Version: 1, RequestID: "durable-last", Durable: true}
	w := httptest.NewRecorder()
	writeRPCBatch(w, responses)
	if w.Body.Len() > maxRPCResponseBytes {
		t.Fatal("batch exceeded wire bound", w.Body.Len())
	}
	var actual []struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil || len(actual) != len(responses) {
		t.Fatal("lost batch responses", err, len(actual))
	}
	capacityErrors := 0
	for i, response := range actual {
		if response.ID != strings.Repeat("<", 245)+fmt.Sprint(i) {
			t.Fatal("lost correlation", i, response.ID)
		}
		if response.Error != nil {
			if len(response.Result) != 0 || response.Error.Code != -32000 || response.Error.Data == nil || response.Error.Data.Code != core.CapacityExceeded {
				t.Fatal("invalid capacity response", response)
			}
			capacityErrors++
		}
	}
	if capacityErrors == 0 {
		t.Fatal("test did not exercise aggregate capacity")
	}
	var admitted core.Result
	if err := json.Unmarshal(actual[len(actual)-1].Result, &admitted); err != nil || !admitted.Durable || admitted.RequestID != "durable-last" {
		t.Fatal("large preceding reads displaced later admission", admitted, err)
	}
}

func TestJSONRPCResponseEncodingFailureKeepsCorrelation(t *testing.T) {
	w := httptest.NewRecorder()
	writeRPCResponse(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage(`"same-id"`), Result: func() {}})
	var response struct {
		ID    string    `json:"id"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.ID != "same-id" || response.Error == nil || response.Error.Data == nil || response.Error.Data.Code != core.CapacityExceeded {
		t.Fatal("encoding failure lost correlation or typed failure", response, err)
	}
}
