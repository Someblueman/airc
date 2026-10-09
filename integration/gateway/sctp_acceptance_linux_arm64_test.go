//go:build linux && arm64

package gateway_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSCTPIPv4NativeOrderedMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeOrderedMessagesAndCursorHistory(t, "ipv4")
}

func TestSCTPIPv6NativeOrderedMessagesAndCursorHistory(t *testing.T) {
	runSCTPNativeOrderedMessagesAndCursorHistory(t, "ipv6")
}

func runSCTPNativeOrderedMessagesAndCursorHistory(t *testing.T, family string) {
	t.Helper()
	f := newSCTPFixture(t, family)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	bodies := []string{
		"SCTP ordered Καλημέρα 世界 🛰️",
		"SCTP long native record one " + strings.Repeat("λ", 1800),
		"SCTP long native record two " + strings.Repeat("λ", 1800),
		"SCTP long native record three " + strings.Repeat("λ", 1800),
		"SCTP long native record four " + strings.Repeat("λ", 1800),
	}
	type expectedPost struct{ id, body string }
	var expected []expectedPost
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
		expected = append(expected, expectedPost{id: seen.ID, body: body})
	}

	// A different operation immediately follows the sends on the same
	// association. The client waits for each complete SCTP response before the
	// next request, matching the one-active-request contract.
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
	largePage := core.Request{Version: 1, RequestID: "sctp-" + family + "-large-response", Operation: core.HistoryRead, Target: target, Limit: 4, Start: "retained"}
	largeResponse, wireBytes := sctpCallMeasured(t, f, f.client, largePage)
	if wireBytes <= 16<<10 || largeResponse.Result.Page == nil || len(largeResponse.Result.Page.Messages) != 4 {
		t.Fatalf("SCTP large sealed response bytes=%d page=%+v; want one response above 16 KiB", wireBytes, largeResponse.Result.Page)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, target.Name)
	if !complete || !reflect.DeepEqual(collected, native) || len(native) != len(expected) {
		t.Fatalf("SCTP bounded cursor history differs: complete=%v pages=%+v native=%+v expected=%+v", complete, collected, native, expected)
	}
	if !reflect.DeepEqual(largeResponse.Result.Page.Messages, native[:4]) {
		t.Fatal("large SCTP response differs from complete native metadata")
	}
	for i, want := range expected {
		if native[i].ID != want.id || native[i].From != "fixture-author" || native[i].Target != target.Name || native[i].Message != want.body {
			t.Fatalf("SCTP independent native message %d=%+v want id=%q body=%q", i, native[i], want.id, want.body)
		}
	}
	if got := f.client; got == nil || got.fd < 0 {
		t.Fatal("SCTP connection did not remain open across ordered requests")
	}
}

func sctpAccepted(t *testing.T, f sctpFixture, conn *sctpTestConn, request core.Request) core.Result {
	t.Helper()
	initial := sctpCall(t, f, conn, request)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatalf("SCTP durable admission=%+v", initial)
	}
	var last core.Result
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		response := sctpCall(t, f, conn, core.Request{Version: 1, RequestID: request.RequestID + "-receipt-" + strconv.Itoa(attempt), Operation: core.ReceiptGet, LookupID: request.RequestID})
		if response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("SCTP receipt lookup=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("SCTP request never reached accepted native delivery: %+v", last)
	return core.Result{}
}
