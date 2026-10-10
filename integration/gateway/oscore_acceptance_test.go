//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOSCOREAuthenticatedSendAndBoundedResume(t *testing.T) {
	f := newOSCOREFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	for i := 0; i < 2; i++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("oscore-bounded-send-%d", i), IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: fmt.Sprintf("Native OSCORE message %d\nwhole Unicode body 🙂", i) + strings.Repeat("o", 3500)}
		accepted := oscoreAccepted(t, f, request)
		if accepted.RequestID != request.RequestID || accepted.MessageID == "" || accepted.SendAttempts != 1 {
			t.Fatal("native acceptance mismatch", accepted)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
		if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
			t.Fatal("OSCORE independent observer mismatch", event)
		}
		replay := oscoreCall(t, f, request).Result.Result
		if replay == nil || replay.State != core.Accepted || replay.MessageID != accepted.MessageID || replay.SendAttempts != 1 {
			t.Fatal("OSCORE stable request replay mismatch", replay)
		}
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 2 {
		t.Fatal("OSCORE replay duplicated native messages", native)
	}
	request := core.Request{Version: 1, RequestID: "oscore-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, record := range native {
		page := oscoreCall(t, f, request).Result.Page
		status := "more"
		if i == len(native)-1 {
			status = "ok"
		}
		if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) || page.Status != status || page.Cursor == "" || page.Gap {
			t.Fatal("OSCORE bounded page differs from native CHECK", page)
		}
		request.Start, request.Cursor = "", page.Cursor
	}
	page := oscoreCall(t, f, request).Result.Page
	if page == nil || len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
		t.Fatal("OSCORE caught-up resume mismatch", page)
	}
}
