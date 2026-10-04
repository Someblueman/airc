//go:build darwin || linux

package gateway_test

import (
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func activityRequest(id, kind, note string, ttl int64) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: core.ActivitySet, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Activity: &core.ActivityInput{Kind: kind, Note: note, TTLSeconds: ttl}}
}

func waitActivityResult(t *testing.T, call func(core.Request, string) adapter.FrameResponse, request core.Request) core.Result {
	t.Helper()
	var last adapter.FrameResponse
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		lookup := core.Request{Version: 1, RequestID: "activity-receipt-" + request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID}
		last = call(lookup, lookup.RequestID)
		if last.Error == nil && last.Result != nil && last.Result.Result != nil && last.Result.Result.State == core.Accepted {
			return *last.Result.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("activity receipt was not accepted: %s response=%+v", request.RequestID, last)
	return core.Result{}
}

func TestActivityMutationOverUnixAndStdio(t *testing.T) {
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	requestTarget := core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newActivityObserver(t, upstream, requestTarget)
	callUnix := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		return readFrame(t, conn)
	}
	request := activityRequest("activity-unix", "typing", "unix typing 🙂", 5)
	response := callUnix(request, request.RequestID)
	if response.Error != nil || response.Result == nil {
		t.Fatalf("unix activity admission: %+v", response)
	}
	ack := waitActivityResult(t, callUnix, request)
	assertActivityMutationFacts(t, ack, request, nextActivitySignal(t, observer))
	stop()

	socket, config, stdioCredentials, stdioObserver := stdioFixtureConfig(t)
	defer stdioObserver.Close()
	observer = newActivityObserver(t, socket, requestTarget)
	process := startStdio(t, gatewayBinary(t), config)
	callStdio := func(request core.Request, correlation, token string) adapter.FrameResponse {
		if token == "" {
			token = stdioCredentials.token(t, request, true)
		}
		process.write(t, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
		return process.read(t)
	}
	replayRequest := activityRequest("activity-stdio-replay", "typing", "stdio typing", 1)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	replayRequest.IssuedAt, replayRequest.ExpiresAt = &issued, &expires
	token := stdioCredentials.token(t, replayRequest, true)
	if response := callStdio(replayRequest, replayRequest.RequestID, token); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio activity admission: %+v", response)
	}
	first := waitActivityResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, replayRequest)
	assertActivityMutationFacts(t, first, replayRequest, nextActivitySignal(t, observer))
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio activity process did not stop: %v", err)
	}
	time.Sleep(2100 * time.Millisecond)
	native := setActivityNative(t, socket, requestTarget, core.ActivityInput{Kind: "thinking", Note: "native newer", TTLSeconds: 15})
	if got := nextActivitySignal(t, observer); got.Action != native.Action || got.Text != native.Text || got.Target != native.Target || got.From != native.From || got.AccountID != native.AccountID || !got.ExpiresAt.Equal(native.ExpiresAt) {
		t.Fatalf("native activity observer mismatch: %+v want=%+v", got, native)
	}
	// Allow the newer signal's throttle to end, so an accidental replay write
	// would broadcast and replace it instead of hiding behind native throttling.
	time.Sleep(2100 * time.Millisecond)
	process = startStdio(t, gatewayBinary(t), config)
	if response := callStdio(replayRequest, "activity-stdio-replay-2", token); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio activity replay: %+v", response)
	}
	replayed := waitActivityResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, replayRequest)
	assertActivityMutationFacts(t, replayed, replayRequest, nil)
	if replayed.SendAttempts != 1 || replayed.LookupAttempts != 0 || *replayed.Activity != *first.Activity {
		t.Fatalf("stdio replay changed saved activity acknowledgement: %+v first=%+v", replayed, first)
	}
	assertNoActivitySignal(t, observer)
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio replay process did not stop: %v", err)
	}
}
