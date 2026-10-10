//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type cardFrameCall func(core.Request, string) adapter.FrameResponse

func waitCardReceipt(t *testing.T, call cardFrameCall, request core.Request) core.CardAcknowledgement {
	t.Helper()
	result := waitCardReceiptResult(t, call, request)
	return *result.Card
}

func waitCardReceiptResult(t *testing.T, call cardFrameCall, request core.Request) *core.Result {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		lookup := core.Request{Version: 1, RequestID: "receipt-" + request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := call(lookup, rand.Text())
		if response.Error == nil && response.Result != nil && response.Result.Result != nil && response.Result.Result.State == core.Accepted {
			if response.Result.Result.Card == nil || response.Result.Result.MessageID != "" || response.Result.Result.Persisted != nil {
				t.Fatalf("card receipt invented message facts: %+v", response.Result.Result)
			}
			return response.Result.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("card receipt was not accepted: %s", request.RequestID)
	return nil
}

func assertCardMutation(t *testing.T, call cardFrameCall, socket string, request core.Request) core.CardAcknowledgement {
	t.Helper()
	response := call(request, request.RequestID)
	if response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("%s was not durably admitted: %+v", request.Operation, response)
	}
	got := waitCardReceipt(t, call, request)
	observed := nativeCard(t, socket, "fixture-author")
	receiptCard, nativeObserved := got.Card, observed
	receiptCard.LastSeen, nativeObserved.LastSeen = time.Time{}, time.Time{}
	if !reflect.DeepEqual(receiptCard, nativeObserved) || observed.LastSeen.Before(got.Card.LastSeen) {
		t.Fatalf("%s receipt/native card mismatch: receipt=%+v native=%+v", request.Operation, got.Card, observed)
	}
	return got
}

func TestProfileAndPresenceOverStdio(t *testing.T) {
	socket, config, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	process := startStdio(t, gatewayBinary(t), config)
	tokens := make(map[string]string)
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		token := credentials.token(t, request, true)
		if request.Operation == core.ProfileUpdate || request.Operation == core.PresenceSet {
			if cached := tokens[request.RequestID]; cached != "" {
				token = cached
			} else {
				tokens[request.RequestID] = token
			}
		}
		process.write(t, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
		response := process.read(t)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("stdio card response correlation/event mismatch: %+v", response)
		}
		return response
	}
	model, about := "stdio model", "stdio profile 🙂"
	profile := core.Request{Version: 1, RequestID: "stdio-card-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model, About: &about}}
	got := assertCardMutation(t, call, socket, profile)
	if got.Card.Model != model || got.Card.About != about || got.Card.Nick != "fixture-author" {
		t.Fatalf("stdio profile acknowledgement lost requested fields: %+v", got)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not restart cleanly: %v", err)
	}
	process = startStdio(t, gatewayBinary(t), config)
	replay := call(profile, "stdio-card-profile-replay")
	if replay.Error != nil || replay.Result == nil || replay.Result.Result == nil || !replay.Result.Result.Durable {
		t.Fatalf("stdio replay of original card token was not durable: %+v", replay)
	}
	replayed := waitCardReceiptResult(t, call, profile)
	if !reflect.DeepEqual(*replayed.Card, got) || replayed.SendAttempts != 1 {
		t.Fatalf("stdio receipt changed after restart: result=%+v before=%+v", replayed, got)
	}
	presence := core.Request{Version: 1, RequestID: "stdio-card-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "thinking", Note: "stdio presence", TTLSeconds: 60}}
	got = assertCardMutation(t, call, socket, presence)
	if got.Card.State != "thinking" || got.Card.Note != "stdio presence" || got.Card.ExpiresAt.IsZero() {
		t.Fatalf("stdio presence acknowledgement lost requested fields: %+v", got)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not stop: %v", err)
	}
}

func TestProfileAndPresenceOverUnixFramed(t *testing.T) {
	conn, credentials, stop, _, socket := unixStreamFixture(t)
	defer conn.Close()
	defer stop()
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		return readFrame(t, conn)
	}
	model, about := "unix model", "unix profile ☃"
	profile := core.Request{Version: 1, RequestID: "unix-card-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model, About: &about}}
	got := assertCardMutation(t, call, socket, profile)
	if got.Card.Model != model || got.Card.About != about {
		t.Fatalf("Unix profile acknowledgement lost requested fields: %+v", got)
	}
	presence := core.Request{Version: 1, RequestID: "unix-card-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", Note: "unix presence", TTLSeconds: 60}}
	got = assertCardMutation(t, call, socket, presence)
	if got.Card.State != "away" || got.Card.Note != "unix presence" || got.Card.ExpiresAt.IsZero() {
		t.Fatalf("Unix presence acknowledgement lost requested fields: %+v", got)
	}
}
