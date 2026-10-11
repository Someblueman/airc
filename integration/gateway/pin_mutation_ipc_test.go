//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func pinRequest(id string, operation core.Operation, parent string) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: operation, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent}
}

func waitFramePinResult(t *testing.T, call func(core.Request, string) adapter.FrameResponse, request core.Request) core.Result {
	t.Helper()
	var last adapter.FrameResponse
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		lookup := core.Request{Version: 1, RequestID: "receipt-" + request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := call(lookup, rand.Text())
		last = response
		if response.Error == nil && response.Result != nil && response.Result.Result != nil && response.Result.Result.State == core.Accepted {
			return *response.Result.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pin receipt was not accepted: %s response=%+v", request.RequestID, last)
	return core.Result{}
}

func nativePinMutation(t *testing.T, socket, target, id, action string) {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: action, Target: target, ID: id}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == action && e.ID == id })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
}

func TestPinMutationOverUnixAndStdio(t *testing.T) {
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	parent := seedPinMutationMessage(t, upstream, "#gateway-fixture", "pin-unix-parent")
	callUnix := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: credentials.token(t, request, true)})
		return readFrame(t, conn)
	}
	add := pinRequest("pin-unix-add", core.PinsAdd, parent)
	if response := callUnix(add, add.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("unix pin add admission: %+v", response)
	}
	assertPinMutationFacts(t, waitFramePinResult(t, callUnix, add), add)
	if pins := queryPinMutationNative(t, upstream, "#gateway-fixture"); len(pins) != 1 || pins[0].ID != parent {
		t.Fatalf("unix native pin missing: %v", pins)
	}
	remove := pinRequest("pin-unix-remove", core.PinsRemove, parent)
	if response := callUnix(remove, remove.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("unix pin remove admission: %+v", response)
	}
	assertPinMutationFacts(t, waitFramePinResult(t, callUnix, remove), remove)
	if pins := queryPinMutationNative(t, upstream, "#gateway-fixture"); len(pins) != 0 {
		t.Fatalf("unix native unpin missing: %v", pins)
	}
	stop()

	socket, config, stdioCredentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	parent = seedPinMutationMessage(t, socket, "#gateway-fixture", "pin-stdio-parent")
	process := startStdio(t, gatewayBinary(t), config)
	callStdio := func(request core.Request, correlation string, requestBody string) adapter.FrameResponse {
		if requestBody == "" {
			requestBody = stdioCredentials.token(t, request, true)
		}
		process.write(t, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: requestBody})
		return process.read(t)
	}
	request := pinRequest("pin-stdio-replay", core.PinsAdd, parent)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request.IssuedAt, request.ExpiresAt = &issued, &expires
	token := stdioCredentials.token(t, request, true)
	if response := callStdio(request, request.RequestID, token); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio pin add admission: %+v", response)
	}
	first := waitFramePinResult(t, func(r core.Request, correlation string) adapter.FrameResponse {
		return callStdio(r, correlation, "")
	}, request)
	assertPinMutationFacts(t, first, request)
	if pins := queryPinMutationNative(t, socket, "#gateway-fixture"); len(pins) != 1 || pins[0].ID != parent {
		t.Fatalf("stdio native add missing: %v", pins)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio first process did not stop: %v", err)
	}
	nativePinMutation(t, socket, "#gateway-fixture", parent, "unpin")
	process = startStdio(t, gatewayBinary(t), config)
	replay := callStdio(request, "pin-stdio-replay-2", token)
	if replay.Error != nil || replay.Result == nil {
		t.Fatalf("stdio replay admission: %+v", replay)
	}
	replayed := waitFramePinResult(t, func(r core.Request, correlation string) adapter.FrameResponse {
		return callStdio(r, correlation, "")
	}, request)
	assertPinMutationFacts(t, replayed, request)
	if replayed.SendAttempts != 1 || replayed.LookupAttempts != 0 {
		t.Fatalf("stdio replay retried pin mutation: %+v", replayed)
	}
	if pins := queryPinMutationNative(t, socket, "#gateway-fixture"); len(pins) != 0 {
		t.Fatalf("stdio accepted add replay repinned message: %v", pins)
	}
	remove = pinRequest("pin-stdio-remove-replay", core.PinsRemove, parent)
	issued, expires = time.Now().Unix(), time.Now().Unix()+120
	remove.IssuedAt, remove.ExpiresAt = &issued, &expires
	removeToken := stdioCredentials.token(t, remove, true)
	if response := callStdio(remove, remove.RequestID, removeToken); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio remove admission: %+v", response)
	}
	removed := waitFramePinResult(t, func(r core.Request, correlation string) adapter.FrameResponse {
		return callStdio(r, correlation, "")
	}, remove)
	assertPinMutationFacts(t, removed, remove)
	if pins := queryPinMutationNative(t, socket, "#gateway-fixture"); len(pins) != 0 {
		t.Fatalf("stdio native remove missing: %v", pins)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio second process did not stop: %v", err)
	}
	nativePinMutation(t, socket, "#gateway-fixture", parent, "pin")
	process = startStdio(t, gatewayBinary(t), config)
	removeReplay := callStdio(remove, "pin-stdio-remove-replay-2", removeToken)
	if removeReplay.Error != nil || removeReplay.Result == nil || removeReplay.Result.Result == nil {
		t.Fatalf("stdio remove replay result: %+v", removeReplay)
	}
	assertPinMutationFacts(t, *removeReplay.Result.Result, remove)
	if pins := queryPinMutationNative(t, socket, "#gateway-fixture"); len(pins) != 1 || pins[0].ID != parent {
		t.Fatalf("stdio accepted remove replay unpinned message: %v", pins)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio replay process did not stop: %v", err)
	}
}
