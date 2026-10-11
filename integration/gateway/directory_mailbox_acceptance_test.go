//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryMailboxNativeAcceptanceRestartReplayAndBoundedHistory(t *testing.T) {
	f := newDirectoryMailboxFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	for i := 0; i < 2; i++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("directory-mailbox-send-%d", i), Operation: core.Send, Target: room, Message: fmt.Sprintf("directory mailbox %d\nwhole Unicode body 🙂", i), IssuedAt: &issued, ExpiresAt: &expires}
		initial := directoryMailboxCall(t, f, request, i == 1).Result.Result
		if initial == nil || !initial.Durable || initial.RequestID != request.RequestID {
			t.Fatal("directory mailbox did not admit", initial)
		}
		var receipt *core.Result
		deadline := time.Now().Add(3 * time.Second)
		for attempt := 0; ; attempt++ {
			receipt = directoryMailboxCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("directory-receipt-%d-%d", i, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
			if receipt != nil && receipt.State == core.Accepted {
				break
			}
			if !time.Now().Before(deadline) {
				t.Fatal("directory native acceptance failed", receipt)
			}
		}
		if receipt.SendAttempts != 1 {
			t.Fatal("unexpected directory send attempts", receipt)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
		if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
			t.Fatal("directory mailbox independent observer mismatch", event)
		}
		f.stop(t)
		f.start(t)
		replay := directoryMailboxCall(t, f, request, i == 1).Result.Result
		if replay == nil || replay.State != core.Accepted || replay.MessageID != receipt.MessageID || replay.SendAttempts != 1 {
			t.Fatal("directory mailbox restart replay changed acceptance", replay, receipt)
		}
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != 2 {
		t.Fatal("directory mailbox replay duplicated native records", native)
	}
	request := core.Request{Version: 1, RequestID: "directory-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, record := range native {
		page := directoryMailboxCall(t, f, request, false).Result.Page
		status := "more"
		if i == len(native)-1 {
			status = "ok"
		}
		if page == nil || page.Status != status || page.Gap || page.Cursor == "" || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) {
			t.Fatal("directory mailbox history mismatch", page, record)
		}
		request.Cursor, request.Start, request.RequestID = page.Cursor, "", fmt.Sprintf("directory-history-%d", i+1)
	}
	page := directoryMailboxCall(t, f, request, false).Result.Page
	if page == nil || page.Status != "ok" || page.Gap || len(page.Messages) != 0 || page.Cursor == "" {
		t.Fatal("directory mailbox cursor did not reach bounded end", page)
	}
}
