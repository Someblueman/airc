//go:build darwin || linux

package gateway_test

import (
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func waitReplySignalResult(t *testing.T, call func(core.Request, string) adapter.FrameResponse, request core.Request) core.Result {
	t.Helper()
	var last adapter.FrameResponse
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		lookup := core.Request{Version: 1, RequestID: "reply-signal-receipt-" + request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID}
		last = call(lookup, lookup.RequestID)
		if last.Error == nil && last.Result != nil && last.Result.Result != nil && last.Result.Result.State == core.Accepted {
			return *last.Result.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	var detail any
	if last.Result != nil {
		detail = last.Result.Result
	}
	t.Fatalf("reply signal receipt was not accepted: %s response=%+v detail=%+v", request.RequestID, last, detail)
	return core.Result{}
}

func assertNativeReplySignal(t *testing.T, signal *irc.SignalEvent, entry irc.ChatEntry) {
	t.Helper()
	if signal.Action != entry.Action || signal.Target != entry.Target || signal.ID != entry.ID || signal.From != entry.From || signal.AccountID != entry.AccountID || signal.Text != entry.Text || !signal.ExpiresAt.Equal(entry.ExpiresAt) {
		t.Fatalf("native reply signal mismatch: signal=%+v entry=%+v", signal, entry)
	}
}

func TestReplySignalOverUnixAndStdio(t *testing.T) {
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	target := core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, upstream, target.Name, "reply-signal-unix-parent")
	observer := newReplySignalObserver(t, upstream, target)
	callUnix := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("Unix reply signal response mismatch: %+v", response)
		}
		return response
	}
	set := replySignalRequest("reply-signal-unix-set", parent, core.ReplySignalSet, &target)
	set.ReplySignal = &core.ReplySignalInput{Note: "unix working", TTLSeconds: 5}
	if response := callUnix(set, set.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("Unix reply signal set admission: %+v", response)
	}
	assertReplySignalFacts(t, waitReplySignalResult(t, callUnix, set), set, nextReplySignal(t, observer, parent))
	cancel := replySignalRequest("reply-signal-unix-cancel", parent, core.ReplySignalCancel, &target)
	if response := callUnix(cancel, cancel.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("Unix reply signal cancel admission: %+v", response)
	}
	assertReplySignalFacts(t, waitReplySignalResult(t, callUnix, cancel), cancel, nextReplySignal(t, observer, parent))
	stop()

	socket, config, stdioCredentials, stdioObserver := stdioFixtureConfig(t)
	defer stdioObserver.Close()
	parent = seedPinMutationMessage(t, socket, target.Name, "reply-signal-stdio-parent")
	observer = newReplySignalObserver(t, socket, target)
	process := startStdio(t, gatewayBinary(t), config)
	callStdio := func(request core.Request, correlation, token string) adapter.FrameResponse {
		if token == "" {
			token = stdioCredentials.token(t, request, true)
		}
		process.write(t, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
		response := process.read(t)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("stdio reply signal response mismatch: %+v", response)
		}
		return response
	}
	set = replySignalRequest("reply-signal-stdio-set", parent, core.ReplySignalSet, &target)
	set.ReplySignal = &core.ReplySignalInput{Note: "stdio working", TTLSeconds: 1}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	set.IssuedAt, set.ExpiresAt = &issued, &expires
	setToken := stdioCredentials.token(t, set, true)
	if response := callStdio(set, set.RequestID, setToken); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio reply signal set admission: %+v", response)
	}
	firstSet := waitReplySignalResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, set)
	assertReplySignalFacts(t, firstSet, set, nextReplySignal(t, observer, parent))
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio set process did not stop: %v", err)
	}
	time.Sleep(2100 * time.Millisecond)
	native := setReplySignalNative(t, socket, parent, nil)
	assertNativeReplySignal(t, nextReplySignal(t, observer, parent), native)
	process = startStdio(t, gatewayBinary(t), config)
	if response := callStdio(set, "reply-signal-stdio-set-replay", setToken); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio set replay admission: %+v", response)
	}
	replayedSet := waitReplySignalResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, set)
	assertReplySignalFacts(t, replayedSet, set, nil)
	if replayedSet.SendAttempts != 1 || replayedSet.LookupAttempts != 0 || *replayedSet.ReplySignal != *firstSet.ReplySignal {
		t.Fatalf("stdio set replay changed saved acknowledgement: %+v first=%+v", replayedSet, firstSet)
	}
	assertNoReplySignal(t, observer)

	cancel = replySignalRequest("reply-signal-stdio-cancel", parent, core.ReplySignalCancel, &target)
	cancel.IssuedAt, cancel.ExpiresAt = &issued, &expires
	cancelToken := stdioCredentials.token(t, cancel, true)
	if response := callStdio(cancel, cancel.RequestID, cancelToken); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio cancel admission: %+v", response)
	}
	firstCancel := waitReplySignalResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, cancel)
	assertReplySignalFacts(t, firstCancel, cancel, nextReplySignal(t, observer, parent))
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio cancel process did not stop: %v", err)
	}
	time.Sleep(2100 * time.Millisecond)
	native = setReplySignalNative(t, socket, parent, &core.ReplySignalInput{Note: "native after cancel", TTLSeconds: 15})
	assertNativeReplySignal(t, nextReplySignal(t, observer, parent), native)
	time.Sleep(2100 * time.Millisecond)
	process = startStdio(t, gatewayBinary(t), config)
	if response := callStdio(cancel, "reply-signal-stdio-cancel-replay", cancelToken); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio cancel replay admission: %+v", response)
	}
	replayedCancel := waitReplySignalResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, cancel)
	assertReplySignalFacts(t, replayedCancel, cancel, nil)
	if replayedCancel.SendAttempts != 1 || replayedCancel.LookupAttempts != 0 || *replayedCancel.ReplySignal != *firstCancel.ReplySignal {
		t.Fatalf("stdio cancel replay changed saved acknowledgement: %+v first=%+v", replayedCancel, firstCancel)
	}
	assertNoReplySignal(t, observer)
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio final process did not stop: %v", err)
	}
}
