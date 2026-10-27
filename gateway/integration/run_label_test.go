package gateway_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// A send or reply with a run label is relayed to AIRC with the +airc/run
// tag, so the message AIRC stores carries the label exactly as a native post
// does; an invalid label, or one on an operation that keeps none, is refused
// before anything is admitted.
func TestHTTPSendCarriesTheRunLabel(t *testing.T) {
	t.Parallel()
	f := gatewayHTTPFixture(t, true)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "run-send", Operation: core.Send, Target: room, Message: "labelled post", Run: "run-2026.10_26"}
	status, result := postAuthorRequest(t, f, send)
	if status != http.StatusAccepted || result.Result == nil || result.Result.State != core.Accepted || result.Result.MessageID == "" {
		t.Fatalf("labelled send: status=%d result=%+v", status, result.Result)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == result.Result.MessageID })
	if seen.Run != send.Run || seen.From != fixtureAuthor || seen.Message != send.Message {
		t.Fatalf("observer saw %+v", seen)
	}
	reply := core.Request{Version: 1, RequestID: "run-reply", Operation: core.Reply, Target: room, ParentID: seen.ID, Message: "labelled reply", Run: "run-reply"}
	status, result = postAuthorRequest(t, f, reply)
	if status != http.StatusAccepted || result.Result == nil || result.Result.State != core.Accepted || result.Result.MessageID == "" {
		t.Fatalf("labelled reply: status=%d result=%+v", status, result.Result)
	}
	replied := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == result.Result.MessageID })
	if replied.Run != reply.Run || replied.ReplyTo != seen.ID {
		t.Fatalf("observer saw reply %+v", replied)
	}
	// The stored messages carry the labels, as history reads them.
	reader := dial(t, f.socket, "run-reader", "", false, true)
	if err := reader.History(room.Name, 50); err != nil {
		t.Fatal(err)
	}
	stored := map[string]string{}
	next(t, reader, func(e irc.Event) bool {
		switch e := e.(type) {
		case *irc.HistoryEvent:
			stored[e.ID] = e.Run
		case *irc.EndOfHistoryEvent:
			return true
		}
		return false
	})
	if stored[seen.ID] != send.Run || stored[replied.ID] != reply.Run {
		t.Fatalf("stored run labels %v", stored)
	}
	// The same request again is the same job; with another label it is
	// another intent.
	if status, again := postAuthorRequest(t, f, send); status != http.StatusAccepted || again.Result == nil || again.Result.MessageID != seen.ID {
		t.Fatalf("labelled send repeated: status=%d result=%+v", status, again.Result)
	}
	relabelled := send
	relabelled.Run = "run-other"
	if status, _ := postAuthorRequest(t, f, relabelled); status != http.StatusConflict {
		t.Fatalf("relabelled request status=%d", status)
	}
	for name, body := range map[string]string{
		"invalid":  `{"version":1,"request_id":"run-bad","operation":"send","target":{"kind":"room","name":"#gateway-fixture"},"message":"x","run":"bad label!"}`,
		"too-long": `{"version":1,"request_id":"run-long","operation":"send","target":{"kind":"room","name":"#gateway-fixture"},"message":"x","run":"` + strings.Repeat("r", 65) + `"}`,
		"reaction": `{"version":1,"request_id":"run-react","operation":"reaction.add","target":{"kind":"room","name":"#gateway-fixture"},"parent_id":"` + seen.ID + `","reaction":"+1","run":"run-1"}`,
		"history":  `{"version":1,"request_id":"run-read","operation":"history.read","target":{"kind":"room","name":"#gateway-fixture"},"limit":10,"run":"run-1"}`,
	} {
		status, _, data := httpCall(t, f, http.MethodPost, "/v1/request", f.token, []byte(body))
		if status != http.StatusBadRequest || !strings.Contains(string(data), string(core.InvalidRequest)) {
			t.Fatalf("%s: status=%d body=%s", name, status, data)
		}
	}
	// Nothing was admitted for the refused requests.
	for _, id := range []string{"run-bad", "run-long", "run-react", "run-read"} {
		if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/requests/"+id, f.token, nil); status != http.StatusNotFound {
			t.Fatalf("%s was admitted: status=%d", id, status)
		}
	}
}
