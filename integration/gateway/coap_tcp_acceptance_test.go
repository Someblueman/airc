//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCoAPSTCPAuthenticatedSendAndBoundedResume(t *testing.T) {
	t.Helper()
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprintf("encrypted=%t", encrypted), func(t *testing.T) {
			f := newCoAPTCPFixture(t)
			room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
			issued := time.Now().Unix()
			expires := issued + 120
			for i := 0; i < 2; i++ {
				request := core.Request{Version: 1, RequestID: fmt.Sprintf("coaps-tcp-bounded-send-%d", i), IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: fmt.Sprintf("CoAPS message %d\nwhole Unicode body 🙂", i)}
				accepted := coapTCPAccepted(t, f, request, encrypted)
				if accepted.RequestID != request.RequestID || accepted.MessageID == "" || accepted.SendAttempts != 1 {
					t.Fatal("CoAPS native acceptance mismatch", accepted)
				}
				replay := coapTCPRPC(t, f, request, encrypted)
				if replay.Result.Result == nil || replay.Result.Result.State != core.Accepted || replay.Result.Result.MessageID != accepted.MessageID || replay.Result.Result.SendAttempts != 1 {
					t.Fatal("CoAPS durable replay mismatch", replay)
				}
				records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
				if len(records) != i+1 || records[i].ID != accepted.MessageID || records[i].Message != request.Message || records[i].From != "fixture-author" || records[i].Target != room.Name {
					t.Fatal("CoAPS native record mismatch", records, accepted)
				}
			}
			native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
			request := core.Request{Version: 1, RequestID: "coaps-tcp-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
			for i, record := range native {
				page := coapTCPRPC(t, f, request, encrypted).Result.Page
				status := "more"
				if i == len(native)-1 {
					status = "ok"
				}
				if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) || page.Status != status || page.Cursor == "" || page.Gap {
					t.Fatal("CoAPS bounded page differs from native CHECK", page)
				}
				request.Start, request.Cursor = "", page.Cursor
			}
			page := coapTCPRPC(t, f, request, encrypted).Result.Page
			if page == nil || len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
				t.Fatal("CoAPS caught-up resume mismatch", page)
			}
		})
	}
}
