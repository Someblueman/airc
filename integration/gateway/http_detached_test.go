package gateway_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func detachedCall(t *testing.T, f httpFixture, token string, encrypted bool) (int, int, dispatch.DetachedResult) {
	t.Helper()
	route := "/v1/envelopes/signed"
	if encrypted {
		route = "/v1/envelopes/encrypted"
	}
	req, _ := http.NewRequest(http.MethodPost, f.server.URL+route, strings.NewReader(token))
	req.Header.Set("Content-Type", "application/jose")
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		scheme := "AIRC-JWS"
		if encrypted {
			scheme = "AIRC-JWE"
		}
		if resp.Header.Get("WWW-Authenticate") != scheme+` realm="airc-gateway"` {
			t.Fatal("detached route advertised wrong authentication", resp.Header)
		}
	}
	var result dispatch.DetachedResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.ProtoMajor, result
}

func TestHTTPDetachedSignedEncryptedDeliveryAndReads(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := httpGatewayFixture(t, version == 2, nil, false, true)
			issued, expiry := time.Now().Unix(), time.Now().Unix()+120
			r := core.Request{Version: 1, RequestID: "detached-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "signed whole\nbody 🙂", IssuedAt: &issued, ExpiresAt: &expiry}
			status, proto, result := detachedCall(t, f, f.credentials.token(t, r, false), false)
			if status != 202 || proto != version || result.Result == nil || !result.Result.Durable {
				t.Fatal("signed HTTP admission failed", status, proto, result)
			}
			job := awaitDelivery(t, f.journal, f.identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
			seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == job.Result.MessageID })
			if seen.From != "fixture-author" || seen.Message != r.Message {
				t.Fatal("signed HTTP native observer mismatch", seen)
			}
			status, _, result = detachedCall(t, f, f.credentials.token(t, r, true), true)
			if status != 202 || result.Result == nil || result.Result.MessageID != seen.ID || result.Result.SendAttempts != 1 {
				t.Fatal("encrypted repeat did not coalesce", status, result)
			}
			status, _, _ = httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r))
			if status != 202 {
				t.Fatal("bearer coalescing failed", status)
			}
			if job.ReplayUntil != expiry+60 {
				t.Fatal("signed replay retention lost", job.ReplayUntil)
			}
			for _, encrypted := range []bool{false, true} {
				for _, op := range []core.Operation{core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.Directory} {
					read := core.Request{Version: 1, RequestID: "read", Operation: op, Limit: 10}
					switch op {
					case core.ReceiptGet:
						read.Limit = 0
						read.LookupID = r.RequestID
					case core.Directory:
						read.Query = "FiXtUrE-AuThOr"
						read.Limit = 1
					default:
						read.Target = r.Target
						if op == core.Search {
							read.Query = "signed whole"
						}
					}
					status, _, result = detachedCall(t, f, f.credentials.token(t, read, encrypted), encrypted)
					if status != 200 || result.Operation != op {
						t.Fatal("detached read failed", encrypted, op, status, result)
					}
					switch op {
					case core.ReceiptGet:
						if result.Result == nil || result.Result.MessageID != seen.ID {
							t.Fatal(result)
						}
					case core.Directory:
						if result.Directory == nil || len(result.Directory.Agents) != 1 || result.Directory.Agents[0].Nick != "fixture-author" {
							t.Fatal(result)
						}
					default:
						if result.Page == nil || len(result.Page.Messages) != 1 || result.Page.Messages[0].ID != seen.ID || !strings.HasPrefix(result.Page.Cursor, "gc1.") {
							t.Fatal("read lost native message or opaque cursor", result)
						}
					}
				}
			}
			r.RequestID, r.Operation, r.ParentID, r.Message = "detached-reply", core.Reply, seen.ID, "encrypted reply"
			status, _, _ = detachedCall(t, f, f.credentials.token(t, r, true), true)
			if status != 202 {
				t.Fatal("encrypted reply admission failed", status)
			}
			job = awaitDelivery(t, f.journal, f.identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
			reply := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == job.Result.MessageID })
			if reply.ReplyTo != seen.ID || reply.ThreadID != seen.ID {
				t.Fatal("encrypted reply linkage failed", reply)
			}
			if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
				t.Fatal(err)
			}
			for _, encrypted := range []bool{false, true} {
				status, _, _ = detachedCall(t, f, f.credentials.token(t, r, encrypted), encrypted)
				if status != 403 {
					t.Fatal("detached HTTP bypassed revocation", status)
				}
			}
		})
	}
}

func TestHTTPDetachedRejectsTamperMixedCredentialsAndOversize(t *testing.T) {
	f := httpGatewayFixture(t, true, nil, false, true)
	r := core.Request{Version: 1, RequestID: "denied-envelope", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
	token := f.credentials.token(t, r, false)
	for _, test := range []struct {
		body, header, value string
		status              int
	}{
		{strings.Repeat("x", 32769), "", "", 413}, {token + "x", "", "", 401},
		{token, "Authorization", "Bearer " + f.token, 400}, {token, "Idempotency-Key", r.RequestID, 400}, {token, "Last-Event-ID", "unsigned", 400},
	} {
		req, _ := http.NewRequest(http.MethodPost, f.server.URL+"/v1/envelopes/signed", bytes.NewBufferString(test.body))
		req.Header.Set("Content-Type", "application/jose")
		if test.header != "" {
			req.Header.Set(test.header, test.value)
		}
		resp, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Fatal("detached denial failed", test.header, resp.StatusCode)
		}
	}
	for _, encrypted := range []bool{false, true} {
		wrong := token
		if !encrypted {
			wrong = f.credentials.token(t, r, true)
		}
		status, _, _ := detachedCall(t, f, wrong, encrypted)
		if status != 401 {
			t.Fatal("wrong envelope profile fell back", encrypted, status)
		}
		forbidden := r
		forbidden.Target = &core.Target{Kind: "room", Name: "#forbidden"}
		status, _, _ = detachedCall(t, f, f.credentials.token(t, forbidden, encrypted), encrypted)
		if status != 403 {
			t.Fatal("signed target permission bypassed", encrypted, status)
		}
	}
	status, _, _ := httpCall(t, f, http.MethodGet, "/v1/requests/"+r.RequestID, f.token, nil)
	if status != 404 {
		t.Fatal("denied envelope created a job", status)
	}
}
