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

func TestFileMailboxNativeAcceptanceStableReplayAndBoundedHistory(t *testing.T) {
	f := newFileMailboxFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	for i := 0; i < 2; i++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("file-mailbox-send-%d", i), Operation: core.Send, Target: room, Message: fmt.Sprintf("file mailbox %d\nwhole Unicode body 🙂", i), IssuedAt: &issued, ExpiresAt: &expires}
		initial := fileMailboxCall(t, f, request, i == 1).Result.Result
		if initial == nil || !initial.Durable || initial.RequestID != request.RequestID {
			t.Fatal("file mailbox did not admit", initial)
		}
		receipt := fileMailboxCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("file-receipt-%d", i), Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
		if receipt == nil || receipt.State != core.Accepted || receipt.SendAttempts != 1 {
			t.Fatal("file mailbox native acceptance failed", receipt)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
		if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
			t.Fatal("file mailbox independent observer mismatch", event)
		}
		replay := fileMailboxCall(t, f, request, i == 1).Result.Result
		if replay == nil || replay.State != core.Accepted || replay.MessageID != receipt.MessageID || replay.SendAttempts != 1 {
			t.Fatal("file mailbox restart replay changed acceptance", replay, receipt)
		}
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != 2 {
		t.Fatal("file mailbox replay duplicated native records", native)
	}
	request := core.Request{Version: 1, RequestID: "file-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, record := range native {
		page := fileMailboxCall(t, f, request, false).Result.Page
		status := "more"
		if i == len(native)-1 {
			status = "ok"
		}
		if page == nil || page.Status != status || page.Gap || page.Cursor == "" || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) {
			t.Fatal("file mailbox history mismatch", page, record)
		}
		request.Cursor, request.Start, request.RequestID = page.Cursor, "", fmt.Sprintf("file-history-%d", i+1)
	}
	page := fileMailboxCall(t, f, request, false).Result.Page
	if page == nil || page.Status != "ok" || page.Gap || len(page.Messages) != 0 || page.Cursor == "" {
		t.Fatal("file mailbox cursor did not reach bounded end", page)
	}
}
