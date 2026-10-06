//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAppendLogNativePartialRestartAndSealedRotation(t *testing.T) {
	f := newAppendLogFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "append-log-partial", Operation: core.Send, Target: room, Message: "partial log completes across restart 🙂"}
	data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "signed", Envelope: f.credentials.token(t, request, false)})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(f.directory, "logs", "00000000000000000001.jsonl")
	if err := os.WriteFile(source, data[:len(data)/2], 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	for _, name := range []string{"claim.json", "cursor.json", "response.json"} {
		if _, err := os.Lstat(filepath.Join(f.directory, name)); !os.IsNotExist(err) {
			t.Fatal("partial record advanced state", name, err)
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 0 {
		t.Fatal("partial record reached native", native)
	}
	f.stop(t)
	f.start(t)
	file, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(data[len(data)/2:], '\n')); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result := appendLogResult(t, f, request.RequestID)
	if result.Error != nil || result.Result == nil || result.Result.Result == nil || !result.Result.Result.Durable {
		t.Fatal("completed partial not admitted", result)
	}
	if err := os.Remove(filepath.Join(f.directory, "response.json")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			request.RequestID, request.Message = "append-log-rotated", "sealed rotation encrypted body 🙂"
			if result := appendLogCall(t, f, request, true).Result.Result; result == nil || !result.Durable {
				t.Fatal("rotated send not admitted", result)
			}
		}
		var receipt *core.Result
		deadline := time.Now().Add(3 * time.Second)
		for {
			receipt = appendLogCall(t, f, core.Request{Version: 1, RequestID: "rotation-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
			if receipt != nil && receipt.State == core.Accepted {
				break
			}
			if !time.Now().Before(deadline) {
				t.Fatal("rotated native acceptance failed", receipt)
			}
		}
		if receipt.SendAttempts != 1 {
			t.Fatal("unexpected rotation attempt count", receipt)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
		if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
			t.Fatal("rotation observer mismatch", event)
		}
		if i == 0 {
			appendLogPublish(t, f, "", nil)
			deadline := time.Now().Add(3 * time.Second)
			for {
				data, err := os.ReadFile(filepath.Join(f.directory, "cursor.json"))
				var checkpoint struct {
					Version int                     `json:"version"`
					Cursor  adapter.AppendLogCursor `json:"cursor"`
				}
				if err == nil && json.Unmarshal(data, &checkpoint) == nil && checkpoint.Version == 1 && checkpoint.Cursor.Sealed {
					break
				}
				if !time.Now().Before(deadline) {
					t.Fatal("seal not checkpointed", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			select {
			case <-f.process.done:
				t.Fatal("absent next generation not idle", f.process.err)
			default:
			}
			f.generation = 2
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 2 {
		t.Fatal("rotation lost or duplicated messages", native)
	}
}
