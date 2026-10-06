//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
)

func TestCoAPSConfirmableAuthenticatedSendAndBoundedResume(t *testing.T) {
	qualifyCoAPS(t, message.Confirmable)
}

func TestCoAPSNonConfirmableAuthenticatedSendAndBoundedResume(t *testing.T) {
	qualifyCoAPS(t, message.NonConfirmable)
}

func qualifyCoAPS(t *testing.T, typ message.Type) {
	t.Helper()
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprintf("encrypted=%t", encrypted), func(t *testing.T) {
			f := newCoAPFixture(t)
			room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
			issued := time.Now().Unix()
			expires := issued + 120
			for i := 0; i < 2; i++ {
				request := core.Request{Version: 1, RequestID: fmt.Sprintf("coaps-bounded-send-%d", i), IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: fmt.Sprintf("CoAPS message %d\nwhole Unicode body 🙂", i)}
				accepted := coapAcceptedType(t, f, request, encrypted, typ)
				if accepted.RequestID != request.RequestID || accepted.MessageID == "" || accepted.SendAttempts != 1 {
					t.Fatal("CoAPS native acceptance mismatch", accepted)
				}
				replay := coapRPCType(t, f, request, encrypted, typ)
				if replay.Result.Result == nil || replay.Result.Result.State != core.Accepted || replay.Result.Result.MessageID != accepted.MessageID || replay.Result.Result.SendAttempts != 1 {
					t.Fatal("CoAPS durable replay mismatch", replay)
				}
				records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
				if len(records) != i+1 || records[i].ID != accepted.MessageID || records[i].Message != request.Message || records[i].From != "fixture-author" || records[i].Target != room.Name {
					t.Fatal("CoAPS native record mismatch", records, accepted)
				}
			}
			native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
			request := core.Request{Version: 1, RequestID: "coaps-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
			for i, record := range native {
				page := coapRPCType(t, f, request, encrypted, typ).Result.Page
				status := "more"
				if i == len(native)-1 {
					status = "ok"
				}
				if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) || page.Status != status || page.Cursor == "" || page.Gap {
					t.Fatal("CoAPS bounded page differs from native CHECK", page)
				}
				request.Start, request.Cursor = "", page.Cursor
			}
			page := coapRPCType(t, f, request, encrypted, typ).Result.Page
			if page == nil || len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
				t.Fatal("CoAPS caught-up resume mismatch", page)
			}
		})
	}
}
