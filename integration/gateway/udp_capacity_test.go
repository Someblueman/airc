//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUDPOversizedHistoryReturnsEncryptedCapacityAndCanResume(t *testing.T) {
	f := newUDPFixture(t)
	exercisePacketHistoryCapacity(t, f)
}

func exercisePacketHistoryCapacity(t *testing.T, f udpFixture) {
	t.Helper()
	socket, room := f.config.Bindings[0].Address, "#gateway-fixture"
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	for index := 0; index < 9; index++ {
		id := fmt.Sprintf("udp-capacity-%d", index)
		if err := author.SendWithID(room, strings.Repeat("x", 4000), id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
		if receipt.ID == "" {
			t.Fatal("native capacity control has no message ID")
		}
	}
	request := core.Request{Version: 1, RequestID: "udp-too-large-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: room}, Start: "retained", Limit: 16}
	writeUDPFrame(t, f, adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	response := readUDPResponse(t, f, time.Now().Add(4*time.Second))
	if response.CorrelationID != request.RequestID || response.Sequence != 1 || response.Event != "error" || response.Error == nil || response.Error.Code != core.CapacityExceeded {
		t.Fatalf("UDP capacity response=%+v error=%+v", response, response.Error)
	}
	request.RequestID, request.Limit = "udp-smaller-history", 1
	page := udpDatagramCall(t, f, request, true)
	native := nativeWebTransportMessages(t, socket, room)
	if len(native) != 9 || page.Result.Page == nil || page.Result.Page.Status != "more" || page.Result.Page.Gap || page.Result.Page.Cursor == "" || len(page.Result.Page.Messages) != 1 || !reflect.DeepEqual(page.Result.Page.Messages[0], native[0]) {
		t.Fatalf("UDP capacity resume lost native record page=%+v native=%d", page, len(native))
	}
	for index := 1; index < len(native); index++ {
		request.RequestID = fmt.Sprintf("udp-smaller-history-%d", index)
		request.Start, request.Cursor = "", page.Result.Page.Cursor
		if request.Cursor == "" {
			t.Fatalf("UDP capacity resume has no cursor at record %d", index)
		}
		page = udpDatagramCall(t, f, request, true)
		status := "more"
		if index == len(native)-1 {
			status = "ok"
		}
		if page.Result.Page == nil || page.Result.Page.Status != status || page.Result.Page.Gap || page.Result.Page.Cursor == "" || len(page.Result.Page.Messages) != 1 || !reflect.DeepEqual(page.Result.Page.Messages[0], native[index]) {
			t.Fatalf("UDP capacity resume lost native record %d: %+v", index, page)
		}
	}
	request.RequestID, request.Start, request.Cursor = "udp-capacity-caught-up", "", page.Result.Page.Cursor
	final := udpDatagramCall(t, f, request, true).Result.Page
	if final == nil || final.Status != "ok" || final.Gap || final.Cursor == "" || len(final.Messages) != 0 {
		t.Fatal("capacity recovery did not reach an explicit whole caught-up page", final)
	}
	rpcNoJobs(t, f.config.StateDir)
}
