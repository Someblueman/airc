//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func oscoreCall(t *testing.T, f *oscoreFixture, r core.Request) adapter.FrameResponse {
	t.Helper()
	frame := frameRequest{Version: 1, CorrelationID: r.RequestID, Profile: "plain", Request: f.credentials.token(t, r, false)}
	request, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	input, output := filepath.Join(root, "request.json"), filepath.Join(root, "response.json")
	if err := os.WriteFile(input, request, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, f.client, "-v", "0", "-B", "10", "-m", "post", "-t", "50", "-b", "256", "-L", "3", "-f", input, "-E", f.peer+","+f.sequence, "-o", output, "coap://"+f.address+"/v1/coap/rpc")
	if diagnostic, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native client: %v %s", err, diagnostic)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("native response: %v %s", err, data)
	}
	if response.Version != 1 || response.CorrelationID != r.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("native RPC failure", response)
	}
	return response
}

func oscoreAccepted(t *testing.T, f *oscoreFixture, r core.Request) core.Result {
	t.Helper()
	initial := oscoreCall(t, f, r)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != r.RequestID {
		t.Fatal("native durable intake", initial)
	}
	deadline := time.Now().Add(8 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := oscoreCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", r.RequestID, attempt), Operation: core.ReceiptGet, LookupID: r.RequestID})
		if receipt.Result.Result == nil || receipt.Result.Result.RequestID != r.RequestID {
			t.Fatal("native receipt", receipt)
		}
		if receipt.Result.Result.State == core.Accepted {
			return *receipt.Result.Result
		}
	}
	t.Fatal("native send did not reach AIRC acceptance")
	return core.Result{}
}
