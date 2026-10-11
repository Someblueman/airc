//go:build linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUnixPacketNativeAcceptanceRestartReplayAndBoundedHistory(t *testing.T) {
	f := newUnixPacketFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	for i := 0; i < 2; i++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("records-send-%d", i), Operation: core.Send, Target: room, Message: fmt.Sprintf("whole sequenced record %d\nUnicode body 🙂", i), IssuedAt: &issued, ExpiresAt: &expires}
		admitted := unixPacketCall(t, f, request).Result.Result
		if admitted == nil || !admitted.Durable || admitted.RequestID != request.RequestID {
			t.Fatal("record send was not durably admitted", admitted)
		}
		receipt := unixPacketReceipt(t, f, request.RequestID)
		if receipt.SendAttempts != 1 || receipt.MessageID == "" {
			t.Fatal("record send acceptance attempts", receipt)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
		if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
			t.Fatal("independent native record observer mismatch", event)
		}
		// Keep an idle accepted connection across whole-gateway shutdown.
		idle := unixPacketDial(t, f)
		probe, err := json.Marshal(frameRequest{Version: 1, CorrelationID: "idle-probe", Profile: "unknown", Request: f.credentials.token(t, request, false)})
		if err != nil {
			t.Fatal(err)
		}
		unixPacketWrite(t, idle, probe)
		if response := unixPacketRead(t, idle); response.CorrelationID != "idle-probe" || response.Error == nil || response.Error.Code != core.InvalidRequest {
			t.Fatal("idle connection was not accepted with an unknown-profile refusal", response)
		}
		f.stop(t)
		if n, _, _, _, err := idle.ReadMsgUnix(make([]byte, 1), nil); n != 0 || err != io.EOF {
			t.Fatal("accepted connection survived shutdown", n, err)
		}
		_ = idle.Close()
		f.start(t)
		replay := unixPacketCall(t, f, request).Result.Result
		if replay == nil || replay.State != core.Accepted || replay.MessageID != receipt.MessageID || replay.SendAttempts != 1 {
			t.Fatal("restart replay changed durable acceptance", replay, receipt)
		}
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(native) != 2 {
		t.Fatal("replayed records duplicated native history", native)
	}
	request := core.Request{Version: 1, RequestID: "records-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, record := range native {
		page := unixPacketCall(t, f, request).Result.Page
		status := "more"
		if i == len(native)-1 {
			status = "ok"
		}
		if page == nil || page.Status != status || page.Gap || page.Cursor == "" || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) {
			t.Fatal("bounded record history disagrees with native history", page, record)
		}
		request.Cursor, request.Start, request.RequestID = page.Cursor, "", fmt.Sprintf("records-history-%d", i+1)
	}
	page := unixPacketCall(t, f, request).Result.Page
	if page == nil || page.Status != "ok" || page.Gap || page.Cursor == "" || len(page.Messages) != 0 {
		t.Fatal("record cursor did not reach bounded end", page)
	}
	f.stop(t)
}
