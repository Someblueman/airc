//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAppendLogNativeExactSizeAndOverflowRecovery(t *testing.T) {
	t.Parallel()
	f := newAppendLogFixture(t)
	request := core.Request{Version: 1, RequestID: "append-log-size-recovery", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "exact append-log input bound recovers after overflow 🙂"}
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)})
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(strings.Repeat(" ", adapter.MaxRequestFrameBytes+1-len(data)))...)
	appendLogPublish(t, f, request.RequestID, data)
	select {
	case <-f.process.done:
		if f.process.err == nil || !strings.Contains(f.process.stderr.String(), "record exceeds frame bound") {
			t.Fatal("oversized append-log input not refused", f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("oversized append-log input did not stop service")
	}
	input := filepath.Join(f.directory, "logs", "00000000000000000001.jsonl")
	retained, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(append(append([]byte(nil), data...), '\n'), retained) {
		t.Fatal("oversized producer input consumed or changed", err)
	}
	for _, name := range []string{"claim.json", "cursor.json", "response.json"} {
		if _, err := os.Lstat(filepath.Join(f.directory, name)); !os.IsNotExist(err) {
			t.Fatal("oversized log advanced state", name, err)
		}
	}
	noMailboxJob(t, f.fileMailboxFixture, request.RequestID)
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(native) != 0 {
		t.Fatal("oversized input reached native history", native)
	}
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	// JSON trailing whitespace keeps the request unchanged.
	data = data[:adapter.MaxRequestFrameBytes]
	response := appendLogDocument(t, f, request.RequestID, data)
	if response.CorrelationID != request.RequestID || response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatal("exact input bound not admitted", response)
	}
	var receipt *core.Result
	deadline := time.Now().Add(3 * time.Second)
	for {
		receipt = appendLogCall(t, f, core.Request{Version: 1, RequestID: "append-log-size-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
		if receipt != nil && receipt.State == core.Accepted {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("exact-size recovery did not reach native acceptance", receipt)
		}
	}
	if receipt.SendAttempts != 1 {
		t.Fatal("size recovery changed native attempt count", receipt)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
		t.Fatal("size recovery observer mismatch", event)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(native) != 1 || native[0].ID != receipt.MessageID {
		t.Fatal("size recovery duplicated native record", native)
	}
}
