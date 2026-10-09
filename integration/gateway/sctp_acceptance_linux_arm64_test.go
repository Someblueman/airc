//go:build linux && arm64

package gateway_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSCTPIPv4NativeOrderedMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeMessagesAndCursorHistory(t, "ipv4", adapter.SCTPOrdered)
}

func TestSCTPIPv6NativeOrderedMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeMessagesAndCursorHistory(t, "ipv6", adapter.SCTPOrdered)
}

func TestSCTPIPv4NativeUnorderedMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeUnorderedMessagesAndCursorHistory(t, "ipv4")
}

func TestSCTPIPv6NativeUnorderedMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeUnorderedMessagesAndCursorHistory(t, "ipv6")
}

func TestSCTPIPv4NativePRTTLMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeMessagesAndCursorHistory(t, "ipv4", adapter.SCTPOrderedTTL)
}

func TestSCTPIPv6NativePRTTLMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeMessagesAndCursorHistory(t, "ipv6", adapter.SCTPOrderedTTL)
}

type sctpExpectedPost struct{ id, body string }

func runSCTPNativeMessagesAndCursorHistory(t *testing.T, family string, mode adapter.SCTPMode) {
	t.Helper()
	f := newSCTPFixtureMode(t, family, mode, true)
	sctpAssertWatchModes(t, f, family)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	bodies := []string{
		"SCTP ordered Καλημέρα 世界 🛰️",
		"SCTP long native record one " + strings.Repeat("λ", 1800),
		"SCTP long native record two " + strings.Repeat("λ", 1800),
		"SCTP long native record three " + strings.Repeat("λ", 1800),
		"SCTP long native record four " + strings.Repeat("λ", 1800),
	}
	var expected []sctpExpectedPost
	for i, body := range bodies {
		issued, expiry := time.Now().Unix(), time.Now().Unix()+120
		request := core.Request{Version: 1, RequestID: "sctp-" + family + "-post-" + strconv.Itoa(i), Operation: core.Send, Target: target, Message: body, IssuedAt: &issued, ExpiresAt: &expiry}
		accepted := sctpAccepted(t, f, f.client, request)
		if accepted.State != core.Accepted || accepted.SendAttempts != 1 || accepted.MessageID == "" {
			t.Fatalf("SCTP accepted receipt=%+v", accepted)
		}
		seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
		if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != body {
			t.Fatalf("SCTP native observer disagrees: %+v", seen)
		}
		expected = append(expected, sctpExpectedPost{id: seen.ID, body: body})
	}
	sctpVerifyHistory(t, f, family, target, expected)
}

func sctpVerifyHistory(t *testing.T, f sctpFixture, family string, target *core.Target, expected []sctpExpectedPost) {
	t.Helper()
	// Exhaust bounded pages after the completed sends on the same association.
	request := core.Request{Version: 1, RequestID: "sctp-" + family + "-history", Operation: core.HistoryRead, Target: target, Limit: 1, Start: "retained"}
	var collected []*irc.MessageMetadata
	complete := false
	for i := 0; i < len(expected)+1; i++ {
		response := sctpCall(t, f, f.client, request)
		page := response.Result.Page
		if page == nil || (page.Status != "ok" && page.Status != "more") || page.Gap || page.Cursor == "" || len(page.Messages) > 1 {
			t.Fatalf("SCTP bounded history page=%+v", page)
		}
		collected = append(collected, page.Messages...)
		request.Start, request.Cursor = "", page.Cursor
		if len(page.Messages) == 0 {
			if page.Status != "ok" {
				t.Fatalf("SCTP empty terminal history status=%q", page.Status)
			}
			complete = true
			break
		}
	}
	largePage := core.Request{Version: 1, RequestID: "sctp-" + family + "-large-response", Operation: core.HistoryRead, Target: target, Limit: len(expected), Start: "retained"}
	largeResponse, wireBytes := sctpCallMeasured(t, f, f.client, largePage)
	if wireBytes <= 16<<10 || largeResponse.Result.Page == nil || len(largeResponse.Result.Page.Messages) != len(expected) {
		t.Fatalf("SCTP large sealed response bytes=%d page=%+v; want one response above 16 KiB", wireBytes, largeResponse.Result.Page)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, target.Name)
	if !complete || !reflect.DeepEqual(collected, native) || len(native) != len(expected) || !reflect.DeepEqual(largeResponse.Result.Page.Messages, native) {
		t.Fatalf("SCTP bounded cursor history differs: complete=%v pages=%+v native=%+v expected=%+v", complete, collected, native, expected)
	}
	if len(largeResponse.Result.Page.Messages) != len(expected) {
		t.Fatalf("large SCTP response message count=%d want=%d", len(largeResponse.Result.Page.Messages), len(expected))
	}
	byID := make(map[string]*irc.MessageMetadata, len(native))
	for _, message := range native {
		byID[message.ID] = message
	}
	for _, want := range expected {
		got := byID[want.id]
		if got == nil || got.From != "fixture-author" || got.Target != target.Name || got.Message != want.body {
			t.Fatalf("SCTP independent native message=%+v want id=%q body=%q", got, want.id, want.body)
		}
	}
	if got := f.client; got == nil || got.fd < 0 {
		t.Fatal("SCTP connection did not remain open across requests")
	}
}

func sctpAssertWatchModes(t *testing.T, f sctpFixture, family string) {
	t.Helper()
	conn := dialSCTP(t, f.address, f.mode)
	response := sctpCall(t, f, conn, core.Request{Version: 1, RequestID: "sctp-" + family + "-capabilities", Operation: core.CapabilitiesGet})
	want := []string{"long_poll", "sctp_ordered_records"}
	if f.mode == adapter.SCTPUnordered {
		want[1] = "sctp_unordered_records"
	} else if f.mode == adapter.SCTPOrderedTTL {
		want[1] = "sctp_pr_ttl_records"
	}
	if response.Result.Capabilities == nil || !reflect.DeepEqual(response.Result.Capabilities.WatchModes, want) {
		t.Fatalf("SCTP advertised watch modes=%v want=%v", response.Result.Capabilities, want)
	}
	if err := conn.close(); err != nil {
		t.Fatal(err)
	}
}

func sctpAccepted(t *testing.T, f sctpFixture, conn *sctpTestConn, request core.Request) core.Result {
	t.Helper()
	initial := sctpCall(t, f, conn, request)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatalf("SCTP durable admission=%+v", initial)
	}
	return sctpWaitAccepted(t, f, conn, request.RequestID)
}

func sctpWaitAccepted(t *testing.T, f sctpFixture, conn *sctpTestConn, requestID string) core.Result {
	t.Helper()
	var last core.Result
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		response := sctpCall(t, f, conn, core.Request{Version: 1, RequestID: requestID + "-receipt-" + strconv.Itoa(attempt), Operation: core.ReceiptGet, LookupID: requestID})
		if response.Result.Result == nil || response.Result.Result.RequestID != requestID {
			t.Fatalf("SCTP receipt lookup=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("SCTP request %q never reached accepted native delivery: %+v", requestID, last)
	return core.Result{}
}
