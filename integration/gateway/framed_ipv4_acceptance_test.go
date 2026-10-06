//go:build darwin || linux

package gateway_test

import (
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestFramedIPv4AuthenticatedSendAndBoundedResume(t *testing.T) {
	conn, credentials, socket, stop := framedFixture(t, false)
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || remote.IP.To4() == nil {
		t.Fatal("fixture did not use IPv4", conn.RemoteAddr())
	}
	observer := dial(t, socket, "framed-ipv4-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "framed-ipv4-observer" })
	call := func(id string, request core.Request, envelope string) *dispatch.DetachedResult {
		t.Helper()
		if envelope == "" {
			envelope = credentials.token(t, request, true)
		}
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: id, Profile: "encrypted", Envelope: envelope})
		response := readFrame(t, conn)
		if response.CorrelationID != id || response.Error != nil || response.Result == nil {
			t.Fatal("IPv4 operation failed", response)
		}
		return response.Result
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	var observed []*irc.MessageEvent
	for _, id := range []string{"ipv4-one", "ipv4-two"} {
		request := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: room, Message: id + "\nwhole body 🙂"}
		envelope := credentials.token(t, request, true)
		admitted := call(id, request, envelope)
		if admitted.Result == nil || !admitted.Result.Durable {
			t.Fatal("IPv4 send was not durable", admitted)
		}
		var accepted *core.Result
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			result := call(id+"-receipt", core.Request{Version: 1, RequestID: id + "-receipt", Operation: core.ReceiptGet, LookupID: id}, "")
			if result.Result != nil && result.Result.State == core.Accepted {
				accepted = result.Result
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if accepted == nil {
			t.Fatal("IPv4 send did not reach native acceptance")
		}
		seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
		if seen.From != "fixture-author" || seen.Target != room.Name || seen.Message != request.Message {
			t.Fatal("IPv4 native observer mismatch", seen)
		}
		observed = append(observed, seen)
		replayed := call(id+"-repeat", request, envelope)
		if replayed.Result == nil || replayed.Result.MessageID != seen.ID || replayed.Result.SendAttempts != 1 {
			t.Fatal("IPv4 stable replay changed acceptance", replayed)
		}
	}
	request := core.Request{Version: 1, RequestID: "ipv4-history", Operation: core.HistoryRead, Target: room, Limit: 1, Start: "retained"}
	for i, seen := range observed {
		result := call("ipv4-page", request, "")
		page := result.Page
		status := "more"
		if i == len(observed)-1 {
			status = "ok"
		}
		if page == nil || len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Messages[0].From != seen.From || page.Messages[0].Target != seen.Target || page.Status != status || page.Cursor == "" || page.Gap {
			t.Fatal("IPv4 bounded page mismatch", page)
		}
		request.Start, request.Cursor = "", page.Cursor
	}
	empty := call("ipv4-caught-up", request, "").Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("IPv4 cursor replayed or lost position", empty)
	}
	stop()
}
