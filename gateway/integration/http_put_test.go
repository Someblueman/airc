package gateway_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPPUTStableRequestResourceAndCrossMethodCoalescing(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := gatewayHTTPFixture(t, version == 2)
			r := core.Request{Version: 1, RequestID: "put-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "PUT whole\nbody 🙂"}
			status, proto, data := httpCall(t, f, http.MethodPut, "/v1/requests/"+r.RequestID, f.token, requestJSON(t, r))
			var result core.Result
			if status != 202 || proto != version || json.Unmarshal(data, &result) != nil || !result.Durable || result.RequestID != r.RequestID {
				t.Fatal("PUT admission failed", status, proto, string(data))
			}
			job := awaitDelivery(t, f.journal, f.identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
			seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == job.Result.MessageID })
			if seen.From != "fixture-author" || seen.Message != r.Message || seen.Target != r.Target.Name {
				t.Fatal("PUT native observer mismatch", seen)
			}
			for _, method := range []string{http.MethodPost, http.MethodPut} {
				path := "/v1/requests/" + r.RequestID
				if method == http.MethodPost {
					path = "/v1/messages"
				}
				status, proto, data = httpCall(t, f, method, path, f.token, requestJSON(t, r))
				if status != 202 || proto != version || json.Unmarshal(data, &result) != nil || !result.Durable || result.State != core.Accepted || result.RequestID != r.RequestID || result.MessageID != seen.ID || result.SendAttempts != 1 {
					t.Fatal("cross-method repeat changed accepted job", method, status, string(data))
				}
			}
			r.Message = "changed intent"
			status, _, _ = httpCall(t, f, http.MethodPut, "/v1/requests/"+r.RequestID, f.token, requestJSON(t, r))
			if status != 409 {
				t.Fatal("PUT changed admitted intent", status)
			}
			status, proto, data = httpCall(t, f, http.MethodGet, "/v1/requests/"+r.RequestID, f.token, nil)
			if status != 200 || proto != version || json.Unmarshal(data, &result) != nil || !result.Durable || result.State != core.Accepted || result.RequestID != r.RequestID || result.MessageID != seen.ID || result.SendAttempts != 1 {
				t.Fatal("rejected PUT changed accepted resource", status, proto, string(data))
			}
			r.RequestID, r.Operation, r.ParentID, r.Message = "put-reply", core.Reply, seen.ID, "PUT reply"
			status, _, _ = httpCall(t, f, http.MethodPut, "/v1/requests/"+r.RequestID, f.token, requestJSON(t, r))
			if status != 202 {
				t.Fatal("PUT reply failed", status)
			}
			job = awaitDelivery(t, f.journal, f.identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
			reply := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == job.Result.MessageID })
			if reply.ReplyTo != seen.ID || reply.ThreadID != seen.ID || reply.Message != r.Message {
				t.Fatal("PUT reply linkage lost", reply)
			}
		})
	}
}

func TestHTTPPUTRejectsIdentityAndPermissionChanges(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	r := core.Request{Version: 1, RequestID: "put-owned", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "PUT identity boundary"}
	for _, tc := range []struct {
		path, token string
		body        []byte
		status      int
	}{
		{"/v1/requests/different", f.token, requestJSON(t, r), 409},
		{"/v1/requests/put-owned", strings.Repeat("f", 64), requestJSON(t, r), 400},
		{"/v1/requests/put-owned", f.token, bytes.Repeat([]byte("x"), core.MaxRequestBytes+1), 413},
		{"/v1/requests/put-owned", f.token, []byte(`{"version":1,"request_id":"put-owned","operation":"directory","limit":1}`), 400},
		{"/v1/requests/put-owned", f.token, []byte(`{"bad":true}`), 400},
		{"/v1/requests/put-owned/extra", f.token, requestJSON(t, r), 404},
		{"/v1/requests/", f.token, requestJSON(t, r), 404},
		{"/v1/requests/put-owned?token=x", f.token, requestJSON(t, r), 403},
	} {
		status, _, data := httpCall(t, f, http.MethodPut, tc.path, tc.token, tc.body)
		if status != tc.status {
			t.Fatal("PUT boundary failed", tc.path, status, string(data))
		}
	}
	for _, keys := range [][]string{{"other"}, {r.RequestID, "other"}} {
		req, _ := http.NewRequest(http.MethodPut, f.server.URL+"/v1/requests/"+r.RequestID, bytes.NewReader(requestJSON(t, r)))
		setAuthorHeaders(req, f.token)
		req.Header.Set("Content-Type", "application/json")
		for _, key := range keys {
			req.Header.Add("Idempotency-Key", key)
		}
		resp, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 409 {
			t.Fatal("PUT idempotency header mismatch admitted", resp.StatusCode)
		}
	}
	status, _, _ := httpCall(t, f, http.MethodGet, "/v1/requests/"+r.RequestID, f.token, nil)
	if status != 404 {
		t.Fatal("rejected PUT created a delivery job", status)
	}
}
