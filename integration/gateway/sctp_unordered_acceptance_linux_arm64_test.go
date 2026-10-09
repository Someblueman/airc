//go:build linux && arm64

package gateway_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func runSCTPNativeUnorderedMessagesAndCursorHistory(t *testing.T, family string) {
	t.Helper()
	f := newSCTPFixtureMode(t, family, true, true)
	sctpAssertWatchModes(t, f, family)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	bodies := []string{
		"SCTP unordered Καλημέρα 世界 🛰️",
		"SCTP unordered long record one " + strings.Repeat("λ", 1800),
	}
	requests := make([]core.Request, len(bodies))
	frames := make([]adapter.FrameRequest, len(bodies))
	expectedIDs := make(map[string]string, len(bodies))
	for i, body := range bodies {
		issued, expiry := time.Now().Unix(), time.Now().Unix()+120
		requests[i] = core.Request{Version: 1, RequestID: "sctp-" + family + "-unordered-" + strconv.Itoa(i), Operation: core.Send, Target: target, Message: body, IssuedAt: &issued, ExpiresAt: &expiry}
		frames[i] = sctpSendRequest(t, f, f.client, requests[i])
		expectedIDs[frames[i].CorrelationID] = requests[i].RequestID
	}
	initial := make(map[string]bool, len(frames))
	for range frames {
		response, _ := sctpReadAnyResponse(t, f, f.client, time.Now().Add(8*time.Second))
		requestID, ok := expectedIDs[response.CorrelationID]
		if !ok || initial[response.CorrelationID] {
			t.Fatalf("unordered SCTP response correlation=%q expected unique one of %v", response.CorrelationID, frames)
		}
		if response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != core.Send || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != requestID {
			t.Fatalf("unordered SCTP durable admission=%+v want request=%q", response, requestID)
		}
		initial[response.CorrelationID] = true
	}
	var expected []sctpExpectedPost
	accepted := make(map[string]core.Result, len(requests))
	for i, request := range requests {
		receipt := sctpWaitAccepted(t, f, f.client, request.RequestID)
		accepted[request.RequestID] = receipt
		expected = append(expected, sctpExpectedPost{id: receipt.MessageID, body: bodies[i]})
	}
	observed := make(map[string]*irc.MessageEvent, len(requests))
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for len(observed) < len(requests) {
		select {
		case event, ok := <-f.observer.Events():
			if !ok {
				t.Fatal("native SCTP observer disconnected")
			}
			message, ok := event.(*irc.MessageEvent)
			if !ok {
				continue
			}
			for requestID, receipt := range accepted {
				if message.ID == receipt.MessageID {
					observed[requestID] = message
				}
			}
		case <-timer.C:
			t.Fatalf("unordered SCTP native observer missed posts: accepted=%+v observed=%+v", accepted, observed)
		}
	}
	for i, request := range requests {
		seen := observed[request.RequestID]
		if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != bodies[i] {
			t.Fatalf("unordered SCTP native observer disagrees: %+v", seen)
		}
	}
	for i := 2; i < 5; i++ {
		body := "SCTP unordered long record " + strconv.Itoa(i) + " " + strings.Repeat("λ", 1800)
		issued, expiry := time.Now().Unix(), time.Now().Unix()+120
		request := core.Request{Version: 1, RequestID: "sctp-" + family + "-unordered-extra-" + strconv.Itoa(i), Operation: core.Send, Target: target, Message: body, IssuedAt: &issued, ExpiresAt: &expiry}
		receipt := sctpAccepted(t, f, f.client, request)
		seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
		if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != body {
			t.Fatalf("unordered SCTP native observer disagrees: %+v", seen)
		}
		expected = append(expected, sctpExpectedPost{id: seen.ID, body: body})
	}
	sctpVerifyHistory(t, f, family, target, expected)
}
