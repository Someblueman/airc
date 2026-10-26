//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

// httpSuiteClient posts each request to POST /v1/request, which takes any
// operation and answers with the DetachedResult every carrier returns.
type httpSuiteClient struct {
	base      string
	transport *http.Transport
}

func dialHTTPSuite(_ *testing.T, d suiteDial) (suiteClient, error) {
	if d.tls != nil {
		return &httpSuiteClient{base: "https://" + d.address, transport: &http.Transport{TLSClientConfig: d.tls}}, nil
	}
	return &httpSuiteClient{base: "http://" + d.address, transport: &http.Transport{}}, nil
}

func (c *httpSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	ctx, cancel := suiteContext(t)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/request", bytes.NewReader(request))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: c.transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return decodeSuiteReply(t, response.StatusCode < 300, data)
}

func (c *httpSuiteClient) send(t *testing.T, request []byte) { c.call(t, request) }

func (c *httpSuiteClient) close() { c.transport.CloseIdleConnections() }

// decodeSuiteReply decodes a DetachedResult, or {"error": Failure} when ok
// is false.
func decodeSuiteReply(t *testing.T, ok bool, data []byte) suiteReply {
	t.Helper()
	if ok {
		var result dispatch.DetachedResult
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("result %s: %v", data, err)
		}
		return suiteReply{result: &result}
	}
	var failure failureBody
	if err := json.Unmarshal(data, &failure); err != nil || failure.Error.Code == "" {
		t.Fatalf("failure %s: %v", data, err)
	}
	return suiteReply{failure: &failure.Error}
}

// httpSuiteWire checks HTTP's own status codes and headers: the resource
// routes, a registered author's receipt needing its token, direct-message
// reads staying blocked, and the access password as a header.
func httpSuiteWire(t *testing.T, e *suiteEnv) {
	base := "http://" + e.address
	id, text := e.id("wire"), e.text("wire")
	var result core.Result
	if status := plainHTTP(t, http.MethodPost, base+"/v1/messages", sendRequest(id, "#wire", text, "alice", aliceToken), nil, &result); status != http.StatusAccepted || result.State != core.Queued && result.State != core.Dispatching && result.State != core.Accepted || result.RequestID != id {
		t.Fatalf("POST /v1/messages: HTTP %d %+v", status, result)
	}
	var failure failureBody
	if status := plainHTTP(t, http.MethodGet, base+"/v1/requests/"+id, nil, authorHeaders("alice", wrongToken), &failure); status != http.StatusUnauthorized || failure.Error.Code != core.AuthenticationFailed {
		t.Fatalf("receipt with a wrong token: HTTP %d %+v", status, failure)
	}
	// The nickname alone is a guest scope that never saw this job.
	if status := plainHTTP(t, http.MethodGet, base+"/v1/requests/"+id, nil, authorHeaders("alice", ""), nil); status != http.StatusNotFound {
		t.Fatal("a registered author's receipt was readable as a guest", status)
	}
	e.posted(t, id, "#wire", text, "alice", aliceToken)
	if status := plainHTTP(t, http.MethodPost, base+"/v1/messages", sendRequest(id, "#wire", "changed", "alice", aliceToken), nil, &failure); status != http.StatusConflict || failure.Error.Code != core.IDConflict {
		t.Fatalf("conflicting retry: HTTP %d %+v", status, failure)
	}
	// The receipts listener admits any target, so only AIRC's rule applies.
	if status := plainHTTP(t, http.MethodGet, e.receipts+"/v1/history?kind=direct&target=alice&limit=5", nil, authorHeaders("alice", aliceToken), nil); status < 400 {
		t.Fatal("direct-message history read allowed", status)
	}

	// A guest's scope is its nickname, case-insensitively, as on IRC.
	id = e.id("Visitor")
	if status := plainHTTP(t, http.MethodPost, base+"/v1/messages", sendRequest(id, "#wire", e.text(id), "Visitor", ""), nil, &result); status != http.StatusAccepted || result.State != core.Queued && result.State != core.Dispatching && result.State != core.Accepted {
		t.Fatalf("guest POST /v1/messages: HTTP %d %+v", status, result)
	}
	if r := waitReceipt(t, base, id, authorHeaders("visitor", "")); r.State != core.Accepted {
		t.Fatalf("guest receipt under another case: %+v", r)
	}

	guarded, nick := "http://"+e.guarded, e.guest("header")
	id = e.id("header")
	request := sendRequest(id, "#wire", "password header", nick, "")
	if status := plainHTTP(t, http.MethodPost, guarded+"/v1/messages", request, nil, &failure); status != http.StatusForbidden || failure.Error.Code != core.Forbidden {
		t.Fatalf("missing access password: HTTP %d %+v", status, failure)
	}
	headers := authorHeaders(nick, "")
	headers["AIRC-Password"] = suitePassword
	if status := plainHTTP(t, http.MethodPost, guarded+"/v1/messages", request, headers, nil); status != http.StatusAccepted {
		t.Fatal("access password header refused", status)
	}
	if r := waitReceipt(t, guarded, id, headers); r.State != core.Accepted {
		t.Fatalf("password-protected post: %+v", r)
	}
	headers["AIRC-Password"] = "wrong"
	if status := plainHTTP(t, http.MethodGet, guarded+"/v1/requests/"+id, nil, headers, nil); status != http.StatusForbidden {
		t.Fatal("wrong access password accepted", status)
	}
}
