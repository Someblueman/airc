//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestStdioPipeAuthenticatedSendAndBoundedResume(t *testing.T) {
	_, c, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	p := startStdio(t, gatewayBinary(t), c)
	correlation := 0
	call := func(id string, request core.Request, requestBody string) *dispatch.DetachedResult {
		t.Helper()
		correlation++
		id = fmt.Sprintf("%s-%d", id, correlation)
		if requestBody == "" {
			requestBody = credentials.token(t, request, true)
		}
		p.write(t, frameRequest{Version: 1, CorrelationID: id, Profile: "plain", Request: requestBody})
		response := p.read(t)
		if response.CorrelationID != id || response.Error != nil || response.Result == nil {
			t.Fatal("framed operation failed", response)
		}
		return response.Result
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	var observed []*irc.MessageEvent
	for _, id := range []string{"framed-one", "framed-two"} {
		request := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: room, Message: id + "\nwhole body 🙂"}
		requestBody := credentials.token(t, request, true)
		admitted := call(id, request, requestBody)
		if admitted.Result == nil || !admitted.Result.Durable {
			t.Fatal("framed send was not durable", admitted)
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
			t.Fatal("framed send did not reach native acceptance")
		}
		seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
		if seen.From != "fixture-author" || seen.Target != room.Name || seen.Message != request.Message {
			t.Fatal("framed native observer mismatch", seen)
		}
		observed = append(observed, seen)
		replayed := call(id+"-repeat", request, requestBody)
		if replayed.Result == nil || replayed.Result.MessageID != seen.ID || replayed.Result.SendAttempts != 1 {
			t.Fatal("framed stable replay changed acceptance", replayed)
		}
	}
	request := core.Request{Version: 1, RequestID: "framed-history", Operation: core.HistoryRead, Target: room, Limit: 1, Start: "retained"}
	for i, seen := range observed {
		result := call("framed-page", request, "")
		page := result.Page
		status := "more"
		if i == len(observed)-1 {
			status = "ok"
		}
		if page == nil || len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Messages[0].From != seen.From || page.Messages[0].Target != seen.Target || page.Status != status || page.Cursor == "" || page.Gap {
			t.Fatal("framed bounded page mismatch", page)
		}
		request.Start, request.Cursor = "", page.Cursor
	}
	empty := call("framed-caught-up", request, "").Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("framed cursor replayed or lost position", empty)
	}
	if err := p.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(); err != nil {
		t.Fatalf("stdio EOF did not join: %v stderr=%s", err, p.stderr.String())
	}
}
