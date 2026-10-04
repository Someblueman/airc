package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPDurableSubmissionStatusAndReplyOverTLS(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := gatewayHTTPFixture(t, version == 2)
			r := core.Request{Version: 1, RequestID: "http-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "HTTP durable post"}
			status, negotiated, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r))
			if status != 202 || negotiated != version {
				t.Fatal("HTTP protocol/admission mismatch", status, negotiated, string(data))
			}
			var admitted core.Result
			if err := json.Unmarshal(data, &admitted); err != nil || !admitted.Durable || admitted.RequestID != r.RequestID {
				t.Fatal("non-durable admission result", admitted, err)
			}
			j := awaitDelivery(t, f.journal, f.identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
			seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == j.Result.MessageID })
			if seen.From != "fixture-author" || seen.Message != r.Message || seen.Target != r.Target.Name {
				t.Fatal("HTTP observer mismatch", seen)
			}
			status, _, data = httpCall(t, f, http.MethodGet, "/v1/requests/"+r.RequestID, f.token, nil)
			var result core.Result
			if err := json.Unmarshal(data, &result); err != nil || status != 200 || result.MessageID != seen.ID {
				t.Fatal("scoped HTTP status lost receipt", status, result, err)
			}
			status, _, _ = httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r))
			if status != 202 {
				t.Fatal("duplicate did not coalesce", status)
			}
			r.Message = "conflicting body"
			status, _, _ = httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r))
			if status != 409 {
				t.Fatal("conflict accepted", status)
			}
			r.RequestID, r.Operation, r.ParentID, r.Message = "http-reply", core.Reply, seen.ID, "HTTP reply"
			status, _, _ = httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r))
			if status != 202 {
				t.Fatal("reply not admitted", status)
			}
			j = awaitDelivery(t, f.journal, f.identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
			reply := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == j.Result.MessageID })
			if reply.ReplyTo != seen.ID || reply.ThreadID != seen.ID || reply.Message != r.Message {
				t.Fatal("HTTP reply linkage mismatch", reply)
			}
		})
	}
}
