//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryMailboxNativeResultQuotaBackpressure(t *testing.T) {
	f := prepareDirectoryMailboxFixture(t)
	for i := 0; i < adapter.MaxDirectoryMailboxFiles; i++ {
		name := fmt.Sprintf("quota-%03d", i)
		data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: name, Profile: "signed", Envelope: "invalid"})
		if err != nil {
			t.Fatal(err)
		}
		directoryMailboxPublish(t, f, name, data)
	}
	f.start(t)
	for i := 0; i < adapter.MaxDirectoryMailboxFiles; i++ {
		name := fmt.Sprintf("quota-%03d", i)
		response := directoryMailboxResult(t, f, name)
		if response.CorrelationID != name || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
			t.Fatal("wrong quota-fill refusal", response)
		}
		noMailboxJob(t, f.fileMailboxFixture, name)
	}
	request := core.Request{Version: 1, RequestID: "quota-healthy", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "healthy after full directory results 🙂"}
	data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "signed", Envelope: f.credentials.token(t, request, false)})
	if err != nil {
		t.Fatal(err)
	}
	directoryMailboxPublish(t, f, request.RequestID, data)
	input := filepath.Join(f.directory, "inbox", request.RequestID+".json")
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		if _, err := os.Stat(input); err != nil {
			t.Fatal("full result quota consumed input", err)
		}
		if entries, err := os.ReadDir(filepath.Join(f.directory, "processing")); err != nil || len(entries) != 0 {
			t.Fatal("full results admitted processing", entries, err)
		}
		select {
		case <-f.process.done:
			t.Fatal("backpressure stopped service", f.process.err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	noMailboxJob(t, f.fileMailboxFixture, request.RequestID)
	if err := os.Remove(filepath.Join(f.directory, "results", "quota-000.json")); err != nil {
		t.Fatal(err)
	}
	response := directoryMailboxResult(t, f, request.RequestID)
	if response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatal("free result slot did not resume intake", response)
	}
	if entries, err := os.ReadDir(filepath.Join(f.directory, "results")); err != nil || len(entries) != adapter.MaxDirectoryMailboxFiles {
		t.Fatal("result count exceeded quota", len(entries), err)
	}
	for i := 1; i < adapter.MaxDirectoryMailboxFiles; i++ {
		if err := os.Remove(filepath.Join(f.directory, "results", fmt.Sprintf("quota-%03d.json", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(f.directory, "results", request.RequestID+".json")); err != nil {
		t.Fatal(err)
	}
	var receipt *core.Result
	deadline := time.Now().Add(3 * time.Second)
	for {
		receipt = directoryMailboxCall(t, f, core.Request{Version: 1, RequestID: "quota-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
		if receipt != nil && receipt.State == core.Accepted {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("quota recovery did not reach native acceptance", receipt)
		}
	}
	if receipt.SendAttempts != 1 {
		t.Fatal("unexpected quota recovery attempt count", receipt)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
		t.Fatal("quota recovery observer mismatch", event)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name); len(native) != 1 || native[0].ID != receipt.MessageID {
		t.Fatal("quota recovery duplicated native record", native)
	}
}
