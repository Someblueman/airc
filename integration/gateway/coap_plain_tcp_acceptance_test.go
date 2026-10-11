//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPlainCoAPTCPAuthenticatedSendAndBoundedResume(t *testing.T) {
	f := newCoAPTCPProfileFixture(t, true)
	caps := plainCoAPTCPCall(t, f, core.Request{Version: 1, RequestID: "plain-coap-capabilities", Operation: core.CapabilitiesGet}).Result.Capabilities
	if caps == nil || len(caps.WatchModes) == 0 || caps.WatchModes[0] != "long_poll" {
		t.Fatal("plain CoAP capability boundary", caps)
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued := time.Now().Unix()
	expires := issued + 120
	for i := 0; i < 2; i++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("plain-coap-tcp-bounded-send-%d", i), IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: fmt.Sprintf("Plain CoAP TCP message %d\nwhole Unicode body 🙂", i) + strings.Repeat("c", 3500)}
		accepted := plainCoAPTCPAccepted(t, f, request)
		if accepted.RequestID != request.RequestID || accepted.MessageID == "" || accepted.SendAttempts != 1 {
			t.Fatal("Plain CoAP TCP native acceptance mismatch", accepted)
		}
		nativeEvent := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == accepted.MessageID })
		if nativeEvent.From != "fixture-author" || nativeEvent.Target != room.Name || nativeEvent.Message != request.Message {
			t.Fatal("CoAP independent observer mismatch", nativeEvent)
		}
		if i == 0 {
			_ = f.conn.Close()
			f.conn = dialPlainCoAPTCP(t, f.address)
		}
		replay := plainCoAPTCPCall(t, f, request)
		if replay.Result.Result == nil || replay.Result.Result.State != core.Accepted || replay.Result.Result.MessageID != accepted.MessageID || replay.Result.Result.SendAttempts != 1 {
			t.Fatal("Plain CoAP TCP durable replay mismatch", replay)
		}
		records := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
		if len(records) != i+1 || records[i].ID != accepted.MessageID || records[i].Message != request.Message || records[i].From != "fixture-author" || records[i].Target != room.Name {
			t.Fatal("Plain CoAP TCP native record mismatch", records, accepted)
		}
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	request := core.Request{Version: 1, RequestID: "plain-coap-tcp-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, record := range native {
		page := plainCoAPTCPCall(t, f, request).Result.Page
		status := "more"
		if i == len(native)-1 {
			status = "ok"
		}
		if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) || page.Status != status || page.Cursor == "" || page.Gap {
			t.Fatal("Plain CoAP TCP bounded page differs from native CHECK", page)
		}
		request.Start, request.Cursor = "", page.Cursor
	}
	page := plainCoAPTCPCall(t, f, request).Result.Page
	if page == nil || len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
		t.Fatal("Plain CoAP TCP caught-up resume mismatch", page)
	}
}
