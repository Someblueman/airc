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

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryMailboxNativeExactSizeAndOverflowRecovery(t *testing.T) {
	f := newDirectoryMailboxFixture(t)
	request := core.Request{Version: 1, RequestID: "directory-size-recovery", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "exact directory input bound recovers after overflow 🙂"}
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)})
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(strings.Repeat(" ", adapter.MaxRequestFrameBytes+1-len(data)))...)
	directoryMailboxPublish(t, f, request.RequestID, data)
	select {
	case <-f.process.done:
		if f.process.err == nil || !strings.Contains(f.process.stderr.String(), "bounded private regular file") {
			t.Fatal("oversized directory input not refused", f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("oversized directory input did not stop service")
	}
	input := filepath.Join(f.directory, "inbox", request.RequestID+".json")
	retained, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(data, retained) {
		t.Fatal("oversized producer input consumed or changed", err)
	}
	for _, name := range []string{"processing", "results"} {
		if entries, err := os.ReadDir(filepath.Join(f.directory, name)); err != nil || len(entries) != 0 {
			t.Fatal("oversized input admitted", name, entries, err)
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
	response := directoryMailboxDocument(t, f, request.RequestID, data)
	if response.CorrelationID != request.RequestID || response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatal("exact input bound not admitted", response)
	}
	var receipt *core.Result
	deadline := time.Now().Add(3 * time.Second)
	for {
		receipt = directoryMailboxCall(t, f, core.Request{Version: 1, RequestID: "directory-size-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
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
