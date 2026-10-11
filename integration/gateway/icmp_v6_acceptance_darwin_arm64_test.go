//go:build darwin && arm64

package gateway_test

import (
	"fmt"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestICMPv6NativeSendReplayAndBoundedHistory(t *testing.T) {
	f := newICMPv6Fixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	requests := make([]core.Request, 0, 6)

	// Read and deliberately discard the first protected receipt. Replaying the
	// exact signed intent with a fresh transport correlation must remain one send.
	first := icmpV6SendRequest(0, room, "ICMPv6 lost receipt 🙂 \"unicode\"")
	requests = append(requests, first)
	frame := icmpV6Frame(t, f, first)
	f.client.sendFrame(t, frame, f.serverChannel, false, false)
	_ = readAGD1Payload(t, f.client, time.Now().Add(6*time.Second))
	replay := frame
	replay.CorrelationID += "-retry"
	response, _ := icmpV6Call(t, f, replay)
	if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != first.RequestID || !response.Result.Result.Durable {
		t.Fatalf("ICMPv6 exact-intent retry result=%+v details=%+v", response.Result, response.Result)
	}
	receipt := icmpV6WaitReceipt(t, f, first.RequestID)
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID == "" {
		t.Fatalf("ICMPv6 replay receipt=%+v", receipt)
	}

	for i := 1; i < 6; i++ {
		request := icmpV6SendRequest(i, room, fmt.Sprintf("ICMPv6 history %d 🙂 λ\n%s", i, strings.Repeat("λ", 1600)))
		requests = append(requests, request)
		accepted, _ := icmpV6Call(t, f, icmpV6Frame(t, f, request))
		if accepted.Result == nil || accepted.Result.Result == nil || accepted.Result.Result.RequestID != request.RequestID || !accepted.Result.Result.Durable {
			t.Fatalf("ICMPv6 native send %d admission=%+v", i, accepted)
		}
		if receipt := icmpV6WaitReceipt(t, f, request.RequestID); receipt.SendAttempts != 1 || receipt.MessageID == "" {
			t.Fatalf("ICMPv6 native send %d receipt=%+v", i, receipt)
		}
	}

	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != len(requests) {
		t.Fatalf("ICMPv6 native observer messages=%d want=%d: %+v", len(native), len(requests), native)
	}
	for i, message := range native {
		if message.From != "fixture-author" || message.Target != room.Name || message.Message != requests[i].Message {
			t.Fatalf("ICMPv6 observer message %d=%+v want author=%q target=%q body=%q", i, message, "fixture-author", room.Name, requests[i].Message)
		}
		observed := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == message.ID })
		if observed.From != "fixture-author" || observed.Target != room.Name || observed.Message != requests[i].Message {
			t.Fatalf("ICMPv6 live native observer event %d=%+v", i, observed)
		}
	}

	caps, _ := icmpV6Call(t, f, icmpV6Frame(t, f, core.Request{Version: 1, RequestID: "icmpv6-capabilities", Operation: core.CapabilitiesGet}))
	if caps.Result == nil || caps.Result.Capabilities == nil || !reflect.DeepEqual(caps.Result.Capabilities.WatchModes, []string{"long_poll", "icmp_v6_mailbox_datagrams"}) {
		t.Fatalf("ICMPv6 advertised capabilities=%+v", caps.Result)
	}

	read := core.Request{Version: 1, RequestID: "icmpv6-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16}
	whole, wireBytes := icmpV6Call(t, f, icmpV6Frame(t, f, read))
	if whole.Result == nil || whole.Result.Page == nil || whole.Result.Page.Status != "ok" || whole.Result.Page.Gap || whole.Result.Page.Cursor == "" || !reflect.DeepEqual(whole.Result.Page.Messages, native) {
		t.Fatalf("ICMPv6 whole history differs from native CHECK: result=%+v native=%+v", whole.Result, native)
	}
	if wireBytes <= 16<<10 {
		t.Fatalf("ICMPv6 protected history wire bytes=%d, want >16KiB", wireBytes)
	}

	bounded := core.Request{Version: 1, RequestID: "icmpv6-history-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, message := range native {
		pageResponse, _ := icmpV6Call(t, f, icmpV6Frame(t, f, bounded))
		if pageResponse.Result == nil {
			t.Fatalf("ICMPv6 bounded response omitted result: %+v", pageResponse)
		}
		page := pageResponse.Result.Page
		wantStatus := "more"
		if i == len(native)-1 {
			wantStatus = "ok"
		}
		if page == nil || page.Status != wantStatus || page.Gap || page.Cursor == "" || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], message) {
			t.Fatalf("ICMPv6 page %d=%+v want message=%+v status=%s", i, page, message, wantStatus)
		}
		bounded.Start, bounded.Cursor, bounded.RequestID = "", page.Cursor, fmt.Sprintf("icmpv6-history-%d", i+1)
	}
	emptyResponse, _ := icmpV6Call(t, f, icmpV6Frame(t, f, bounded))
	if emptyResponse.Result == nil {
		t.Fatalf("ICMPv6 caught-up response omitted result: %+v", emptyResponse)
	}
	if empty := emptyResponse.Result.Page; empty == nil || empty.Status != "ok" || empty.Gap || len(empty.Messages) != 0 || empty.Cursor == "" {
		t.Fatalf("ICMPv6 caught-up cursor page=%+v", empty)
	}
	if f.client.autoReplies == 0 {
		t.Fatal("native ICMPv6 client did not observe and ignore any kernel EchoReply packets")
	}

	stopICMPv6Gateway(t, f)
}

func TestICMPv6RefusalBoundsAndRecovery(t *testing.T) {
	f := newICMPv6Fixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	base := icmpV6SendRequest(20, room, "must not reach native")
	unknownRequest := withICMPv6RequestID(base, "icmpv6-unknown-profile")
	for _, frame := range []frameRequest{
		{Version: 1, CorrelationID: "icmpv6-malformed", Profile: "plain", Request: "[]"},
		{Version: 1, CorrelationID: "icmpv6-unknown-profile", Profile: "unknown", Request: f.credentials.token(t, unknownRequest, false)},
	} {
		f.client.sendFrame(t, frame, f.serverChannel, false, false)
		icmpV6ExpectNoResponse(t, f)
	}

	// A correctly addressed request is silent if its ICMP Echo ID names another
	// cooperative channel; the kernel's type-129 EchoReply is filtered by client.Read.
	wrongChannel := icmpV6Frame(t, f, withICMPv6RequestID(base, "icmpv6-wrong-channel"))
	f.client.sendFrame(t, wrongChannel, f.serverChannel+2, false, false)
	icmpV6ExpectNoResponse(t, f)

	oversized := frameRequest{Version: 1, CorrelationID: "icmpv6-oversized", Profile: "plain", Request: `"` + strings.Repeat("x", adapter.MaxRequestFrameBytes) + `"`}
	f.client.sendFrame(t, oversized, f.serverChannel, false, true)
	icmpV6ExpectNoResponse(t, f)

	partial := icmpV6Frame(t, f, withICMPv6RequestID(base, "icmpv6-partial"))
	f.client.sendFrame(t, partial, f.serverChannel, true, false)
	icmpV6ExpectNoResponse(t, f)
	if records := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(records) != 0 {
		t.Fatalf("refused ICMPv6 frames reached native history: %+v", records)
	}
	rpcNoJobs(t, f.config.StateDir)

	// Retry the exact retained signed intent with a fresh AGD1 delivery ID after
	// the partial attempt; this proves intake resumes without changing intent.
	response, _ := icmpV6Call(t, f, partial)
	if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != "icmpv6-partial" || !response.Result.Result.Durable {
		t.Fatalf("ICMPv6 exact-request retry after partial loss=%+v", response)
	}
	receipt := icmpV6WaitReceipt(t, f, "icmpv6-partial")
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID == "" {
		t.Fatalf("ICMPv6 exact-request retry receipt=%+v", receipt)
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != 1 || native[0].ID != receipt.MessageID || native[0].From != "fixture-author" || native[0].Target != room.Name || native[0].Message != base.Message {
		t.Fatalf("ICMPv6 post-refusal native control=%+v", native)
	}
	stopICMPv6Gateway(t, f)
}

func icmpV6SendRequest(index int, target *core.Target, message string) core.Request {
	return core.Request{Version: 1, RequestID: fmt.Sprintf("icmpv6-send-%d", index), Operation: core.Send, Target: target, Message: message}
}

func withICMPv6RequestID(request core.Request, id string) core.Request {
	request.RequestID = id
	return request
}

func icmpV6WaitReceipt(t *testing.T, f icmpV6Fixture, requestID string) core.Result {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("icmpv6-receipt-%d", attempt), Operation: core.ReceiptGet, LookupID: requestID}
		response, _ := icmpV6Call(t, f, icmpV6Frame(t, f, request))
		if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != requestID {
			t.Fatalf("ICMPv6 receipt response=%+v", response.Result)
		}
		if response.Result.Result.State == core.Accepted {
			return *response.Result.Result
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("ICMPv6 receipt did not reach acceptance for %q", requestID)
	return core.Result{}
}

func stopICMPv6Gateway(t *testing.T, f icmpV6Fixture) {
	t.Helper()
	if err := f.process.command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("interrupt ICMPv6 gateway: %v", err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("ICMPv6 gateway shutdown: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(7 * time.Second):
		t.Fatalf("ICMPv6 gateway did not join after shutdown: stderr=%s", f.process.stderr.String())
	}
}
