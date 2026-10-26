//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func seedPinMutationMessage(t *testing.T, socket, target, requestID string) string {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.SendWithID(target, "pin mutation "+requestID, requestID); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == requestID })
	if receipt.ID == "" {
		t.Fatal("native pin seed lacks message ID")
	}
	return receipt.ID
}

func queryPinMutationNative(t *testing.T, socket, target string) []irc.MessageMetadata {
	t.Helper()
	reader := dial(t, socket, fmt.Sprintf("pin-reader-%d", roomReaderSequence.Add(1)), "", false, false)
	defer reader.Close()
	if err := reader.Chat(irc.ChatRequest{Action: "pins", Target: target}); err != nil {
		t.Fatal(err)
	}
	pins := make([]irc.MessageMetadata, 0)
	for {
		event := next(t, reader, func(e irc.Event) bool {
			switch e.(type) {
			case *irc.ChatEvent, *irc.EndOfChatEvent:
				return true
			}
			return false
		})
		switch value := event.(type) {
		case *irc.ChatEvent:
			if value.Action != "pin" || value.Target != target || value.Message == nil || value.Message.Target != target || value.ID != value.Message.ID {
				t.Fatalf("foreign native pin snapshot: %+v", value)
			}
			pins = append(pins, *value.Message)
		case *irc.EndOfChatEvent:
			return pins
		}
	}
}

func assertPinMutationFacts(t *testing.T, result core.Result, request core.Request) {
	t.Helper()
	expected := core.PinAcknowledgement{Version: 1, Operation: request.Operation, Target: *request.Target, ParentID: request.ParentID}
	if result.State != core.Accepted || !result.Durable || result.Accepted == nil || !*result.Accepted || result.Pin == nil || *result.Pin != expected || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil || result.Card != nil || result.Topic != nil || result.Room != nil {
		t.Fatalf("pin mutation facts: %+v expected=%+v", result, expected)
	}
}

func waitHTTPPinResult(t *testing.T, f *reactionTransportFixture, id string, state core.State) core.Result {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, data := headerRead(t, f, "/v1/requests/"+id)
		var result core.Result
		if status == http.StatusOK && json.Unmarshal(data, &result) == nil && result.State == state {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatalf("pin %s wanted %s: status=%d body=%s", id, state, status, data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
