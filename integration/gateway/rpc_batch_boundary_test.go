package gateway_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRPCQuietBatchOutlivesOrdinaryHTTPWriteDeadline(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	var entries []json.RawMessage
	for _, id := range []string{"first", "second", "third"} {
		entries = append(entries, rpcRequestBody(t, id, "airc.request", core.Request{Version: 1, RequestID: "quiet-" + id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}))
	}
	body, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	status, protocol, data := httpCall(t, f, http.MethodPost, "/v1/rpc", f.token, body)
	if status != http.StatusOK || protocol != 2 || time.Since(started) < 5*time.Second {
		t.Fatal("batch did not complete beyond ordinary five-second write bound", status, protocol, time.Since(started), string(data))
	}
	var responses []rpcResponse
	if err := json.Unmarshal(data, &responses); err != nil || len(responses) != 3 {
		t.Fatal("lost quiet batch responses", err, string(data))
	}
	for i, response := range responses {
		assertRPCID(t, response.ID, []string{"first", "second", "third"}[i])
		var page liveWirePage
		if err := json.Unmarshal(response.Result, &page); err != nil || len(response.Error) != 0 || len(page.Messages) != 0 || page.Status != "ok" {
			t.Fatal("quiet poll failed or invented data", response, page, err)
		}
	}
	rpcNoJobs(t, f.stateDir)
}

func TestRPCBatchRechecksRevocationBetweenEntries(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	entries := []json.RawMessage{
		rpcRequestBody(t, "authorized-watch", "airc.request", core.Request{Version: 1, RequestID: "batch-watch", Operation: core.Watch, Target: room, Limit: 1}),
		rpcRequestBody(t, "revoked-send", "airc.request", core.Request{Version: 1, RequestID: "batch-send", Operation: core.Send, Target: room, Message: "must not post"}),
	}
	body, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+"/v1/rpc", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/json")
	type outcome struct {
		body []byte
		err  error
	}
	done := make(chan outcome, 1)
	joined := make(chan struct{})
	baseline := nativeObserverCount(t, f.socket)
	go func() {
		defer close(joined)
		response, err := f.server.Client().Do(request)
		if err != nil {
			done <- outcome{err: err}
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		done <- outcome{body: data, err: err}
	}()
	defer func() { cancel(); <-joined }()
	waitNativeObserverCount(t, f.socket, baseline+1)
	if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
		t.Fatal(err)
	}
	result := <-done
	var responses []struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int          `json:"code"`
			Data core.Failure `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(result.body, &responses); result.err != nil || err != nil || len(responses) != 2 {
		t.Fatal("batch failed after revocation", result.err, err, string(result.body))
	}
	if responses[0].ID != "authorized-watch" || responses[0].Error != nil || len(responses[0].Result) == 0 || responses[1].ID != "revoked-send" || responses[1].Error == nil || responses[1].Error.Code != -32000 || responses[1].Error.Data.Code != core.Forbidden {
		t.Fatal("batch reused stale authorization", string(result.body))
	}
	waitNativeObserverCountAtMost(t, f.socket, baseline)
	rpcNoJobs(t, f.stateDir)
}

func rpcNoJobs(t *testing.T, stateDir string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(stateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal("read or denied request created a delivery job", count, err)
	}
}
