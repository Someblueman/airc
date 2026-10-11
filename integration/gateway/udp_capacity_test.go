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

func TestUDPOversizedHistoryReturnsBoundedPageAndCanResume(t *testing.T) {
	f := newUDPFixture(t)
	exercisePacketHistoryCapacity(t, f)
}

func exercisePacketHistoryCapacity(t *testing.T, f udpFixture) {
	t.Helper()
	socket, room := f.config.Upstream.Address, "#gateway-fixture"
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	for index := 0; index < 16; index++ {
		id := fmt.Sprintf("udp-capacity-%d", index)
		if err := author.SendWithID(room, strings.Repeat("x", 4000), id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
		if receipt.ID == "" {
			t.Fatal("native capacity control has no message ID")
		}
	}
	request := core.Request{Version: 1, RequestID: "udp-too-large-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: room}, Start: "retained", Limit: 32}
	writeUDPFrame(t, f, frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)})
	// Sixteen 4000-byte records exceed one response: the reply is either a
	// partial page that reports more, or a whole-page capacity error, never a
	// truncated record.
	response := readUDPResponse(t, f, time.Now().Add(4*time.Second))
	partial := response.Event == "result" && response.Result != nil && response.Result.Page != nil && response.Result.Page.Status == "more" && len(response.Result.Page.Messages) > 0 && len(response.Result.Page.Messages) < 16
	capacity := response.Event == "error" && response.Error != nil && response.Error.Code == core.CapacityExceeded
	if response.CorrelationID != request.RequestID || response.Sequence != 1 || !partial && !capacity {
		t.Fatalf("UDP bounded history response=%+v error=%+v", response, response.Error)
	}
	request.RequestID, request.Limit = "udp-smaller-history", 1
	page := udpDatagramCall(t, f, request, true)
	native := nativeWebTransportMessages(t, socket, room)
	if len(native) != 16 || page.Result.Page == nil || page.Result.Page.Status != "more" || page.Result.Page.Gap || page.Result.Page.Cursor == "" || len(page.Result.Page.Messages) != 1 || !reflect.DeepEqual(page.Result.Page.Messages[0], native[0]) {
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
