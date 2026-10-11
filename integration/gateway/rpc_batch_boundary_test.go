package gateway_test

import (
	"database/sql"
	"encoding/json"
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
		entries = append(entries, rpcRequestBody(t, id, "airc.request", rpcRequestParams(core.Request{Version: 1, RequestID: "quiet-" + id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1})))
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
		if err := json.Unmarshal(detachedPayload(response.Result), &page); err != nil || len(response.Error) != 0 || len(page.Messages) != 0 || page.Status != "ok" {
			t.Fatal("quiet poll failed or invented data", response, page, err)
		}
	}
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
