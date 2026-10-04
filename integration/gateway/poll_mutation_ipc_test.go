//go:build darwin || linux

package gateway_test

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func waitPollMutationResult(t *testing.T, call func(core.Request, string) adapter.FrameResponse, request core.Request) core.Result {
	t.Helper()
	var last adapter.FrameResponse
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		lookup := core.Request{Version: 1, RequestID: "poll-receipt-" + request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID}
		last = call(lookup, lookup.RequestID)
		if last.Error == nil && last.Result != nil && last.Result.Result != nil && last.Result.Result.State == core.Accepted {
			return *last.Result.Result
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("poll receipt not accepted: %s response=%+v", request.RequestID, last)
	return core.Result{}
}

func pollNativeVote(t *testing.T, socket, pollID string, choice int) irc.ChatEntry {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "vote", ID: pollID, Choice: choice}); err != nil {
		t.Fatal(err)
	}
	entry := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" && e.ID == pollID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	return entry.ChatEntry
}

func pollObserverEntry(t *testing.T, observer *irc.Client, pollID string, signal bool) irc.ChatEntry {
	t.Helper()
	if signal {
		return next(t, observer, func(e *irc.SignalEvent) bool { return e.Action == "results" && e.ID == pollID }).ChatEntry
	}
	event := next(t, observer, func(e *irc.MessageEvent) bool { return e.Kind == "poll" && e.ID == pollID })
	return irc.ChatEntry{Action: "results", Target: event.Target, ID: event.ID, From: event.From, AccountID: event.AccountID, Text: event.Message, Options: event.PollOptions, Votes: make([]int, len(event.PollOptions)), ExpiresAt: event.PollClosesAt}
}

func assertNoPollMutation(t *testing.T, observer *irc.Client, pollID string) {
	t.Helper()
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("poll observer disconnected")
			}
			switch value := event.(type) {
			case *irc.MessageEvent:
				if value.Kind == "poll" {
					t.Fatalf("poll replay created native poll: %+v", value)
				}
			case *irc.SignalEvent:
				if value.ID == pollID && value.Action == "results" {
					t.Fatalf("poll replay changed native result: %+v", value)
				}
			}
		case <-timer.C:
			return
		}
	}
}

func TestPollMutationOverUnixAndStdio(t *testing.T) {
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, upstream, *target)
	callUnix := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("Unix poll response mismatch: %+v", response)
		}
		return response
	}
	create := pollMutationRequest("poll-unix-create", core.PollCreate, target, "")
	create.Poll = &core.PollInput{Question: "Unix poll 🙂", Options: []string{"yes", "no"}, TTLSeconds: 60}
	if response := callUnix(create, create.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("Unix poll create admission: %+v", response)
	}
	created := waitPollMutationResult(t, callUnix, create)
	createdNative := pollObserverEntry(t, observer, created.Poll.Poll.ID, false)
	assertPollMutationFacts(t, created, create, &createdNative)
	vote := pollMutationRequest("poll-unix-vote", core.PollVote, target, created.Poll.Poll.ID)
	vote.Choice = 1
	if response := callUnix(vote, vote.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("Unix poll vote admission: %+v", response)
	}
	voted := waitPollMutationResult(t, callUnix, vote)
	votedNative := pollObserverEntry(t, observer, created.Poll.Poll.ID, true)
	assertPollMutationFacts(t, voted, vote, &votedNative)
	closeRequest := pollMutationRequest("poll-unix-close", core.PollClose, target, created.Poll.Poll.ID)
	if response := callUnix(closeRequest, closeRequest.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("Unix poll close admission: %+v", response)
	}
	closed := waitPollMutationResult(t, callUnix, closeRequest)
	closedNative := pollObserverEntry(t, observer, created.Poll.Poll.ID, true)
	assertPollMutationFacts(t, closed, closeRequest, &closedNative)
	stop()

	socket, config, stdioCredentials, stdioObserver := stdioFixtureConfig(t)
	defer stdioObserver.Close()
	observer = newReplySignalObserver(t, socket, *target)
	process := startStdio(t, gatewayBinary(t), config)
	callStdio := func(request core.Request, correlation, token string) adapter.FrameResponse {
		if token == "" {
			token = stdioCredentials.token(t, request, true)
		}
		process.write(t, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
		response := process.read(t)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("stdio poll response mismatch: %+v", response)
		}
		return response
	}
	create = pollMutationRequest("poll-stdio-replay", core.PollCreate, target, "")
	create.Poll = &core.PollInput{Question: "stdio poll replay", Options: []string{"one", "two"}, TTLSeconds: 60}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	create.IssuedAt, create.ExpiresAt = &issued, &expires
	token := stdioCredentials.token(t, create, true)
	if response := callStdio(create, create.RequestID, token); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio poll create admission: %+v", response)
	}
	first := waitPollMutationResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, create)
	firstNative := pollObserverEntry(t, observer, first.Poll.Poll.ID, false)
	assertPollMutationFacts(t, first, create, &firstNative)
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio poll process did not stop: %v", err)
	}
	nativeVote := pollNativeVote(t, socket, first.Poll.Poll.ID, 2)
	nativeSignal := pollObserverEntry(t, observer, first.Poll.Poll.ID, true)
	if !reflect.DeepEqual(nativeVote, nativeSignal) {
		t.Fatalf("native independent vote mismatch: vote=%+v signal=%+v", nativeVote, nativeSignal)
	}
	process = startStdio(t, gatewayBinary(t), config)
	if response := callStdio(create, "poll-stdio-replay-2", token); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio poll replay admission: %+v", response)
	}
	replayed := waitPollMutationResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, create)
	assertPollMutationFacts(t, replayed, create, nil)
	if !reflect.DeepEqual(replayed, first) || replayed.SendAttempts != 1 || replayed.LookupAttempts != 0 {
		t.Fatalf("stdio poll replay changed saved result: first=%+v replay=%+v", first, replayed)
	}
	assertNoPollMutation(t, observer, first.Poll.Poll.ID)
	vote = pollMutationRequest("poll-stdio-vote", core.PollVote, target, first.Poll.Poll.ID)
	vote.Choice = 1
	if response := callStdio(vote, vote.RequestID, ""); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio poll vote admission: %+v", response)
	}
	votedNative = pollObserverEntry(t, observer, first.Poll.Poll.ID, true)
	assertPollMutationFacts(t, waitPollMutationResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, vote), vote, &votedNative)
	closeRequest = pollMutationRequest("poll-stdio-close", core.PollClose, target, first.Poll.Poll.ID)
	if response := callStdio(closeRequest, closeRequest.RequestID, ""); response.Error != nil || response.Result == nil {
		t.Fatalf("stdio poll close admission: %+v", response)
	}
	closedNative = pollObserverEntry(t, observer, first.Poll.Poll.ID, true)
	assertPollMutationFacts(t, waitPollMutationResult(t, func(r core.Request, correlation string) adapter.FrameResponse { return callStdio(r, correlation, "") }, closeRequest), closeRequest, &closedNative)
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio final poll process did not stop: %v", err)
	}
}
