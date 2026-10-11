//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryMailboxNativeInboxQuotaRefusalAndRecovery(t *testing.T) {
	f := prepareDirectoryMailboxFixture(t)
	request := core.Request{Version: 1, RequestID: "inbox-quota-message", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "one intent across bounded inbox names 🙂"}
	token := f.credentials.token(t, request, false)
	for i := 0; i <= adapter.MaxDirectoryMailboxFiles; i++ {
		name := fmt.Sprintf("inbox-%03d", i)
		// Correlation names identify transport slots; every frame carries the
		// same durable intent so admitted retries must still produce one record.
		data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: name, Profile: "plain", Request: token})
		if err != nil {
			t.Fatal(err)
		}
		directoryMailboxPublish(t, f, name, data)
	}
	f.process = startGateway(t, f.binary, f.config)
	select {
	case <-f.process.done:
		if f.process.err == nil || !strings.Contains(f.process.stderr.String(), "file quota exceeded") {
			t.Fatal("over-quota inbox not refused", f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("over-quota inbox did not stop service")
	}
	for name, count := range map[string]int{"inbox": adapter.MaxDirectoryMailboxFiles + 1, "processing": 0, "results": 0} {
		entries, err := os.ReadDir(filepath.Join(f.directory, name))
		if err != nil || len(entries) != count {
			t.Fatal("quota refusal consumed input", name, len(entries), err)
		}
	}
	noMailboxJob(t, f.fileMailboxFixture, request.RequestID)
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(native) != 0 {
		t.Fatal("quota refusal reached native history", native)
	}
	if err := os.Remove(filepath.Join(f.directory, "inbox", "inbox-128.json")); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	for i := 0; i < adapter.MaxDirectoryMailboxFiles; i++ {
		name := fmt.Sprintf("inbox-%03d", i)
		response := directoryMailboxResult(t, f, name)
		if response.CorrelationID != name || response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != request.RequestID {
			t.Fatal("exact inbox quota did not admit stable intent", response)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(f.directory, "results")); err != nil || len(entries) != adapter.MaxDirectoryMailboxFiles {
		t.Fatal("wrong recovered result quota", len(entries), err)
	}
	for i := 0; i < adapter.MaxDirectoryMailboxFiles; i++ {
		if err := os.Remove(filepath.Join(f.directory, "results", fmt.Sprintf("inbox-%03d.json", i))); err != nil {
			t.Fatal(err)
		}
	}
	var receipt *core.Result
	deadline := time.Now().Add(3 * time.Second)
	for {
		receipt = directoryMailboxCall(t, f, core.Request{Version: 1, RequestID: "inbox-quota-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
		if receipt != nil && receipt.State == core.Accepted {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("inbox quota recovery did not reach native acceptance", receipt)
		}
	}
	if receipt.SendAttempts != 1 {
		t.Fatal("inbox retries changed native attempt count", receipt)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
		t.Fatal("inbox quota observer mismatch", event)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(native) != 1 || native[0].ID != receipt.MessageID {
		t.Fatal("inbox quota retries duplicated native record", native)
	}
}
