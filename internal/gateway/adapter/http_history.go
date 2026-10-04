package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func httpReadPage(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, token string) {
	search := r.URL.Path == "/v1/search"
	if len(r.URL.RawQuery) > 4096 {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	for key, values := range query {
		if len(values) != 1 || !utf8.ValidString(values[0]) || key != "kind" && key != "target" && key != "limit" && key != "cursor" && !(key == "start" && !search) && !((key == "query" || key == "from") && search) {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
	}
	limit := 50
	if raw, ok := query["limit"]; ok {
		limit, err = strconv.Atoi(raw[0])
		if err != nil {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
	}
	request := core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}, Limit: limit, Cursor: query.Get("cursor")}
	if search {
		request.Operation = core.Search
		request.RequestID = "search"
		request.Query = query.Get("query")
		request.From = query.Get("from")
	}
	if r.URL.Path == "/v1/watch" {
		request.Operation, request.RequestID = core.Watch, "watch"
	}
	body, err := json.Marshal(request)
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	// Leave room within the server's five-second write bound for encoding errors.
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	page, err := d.ReadPage(ctx, token, body, query.Get("start"), time.Now())
	if err != nil {
		status, code := httpError(err)
		httpFailure(w, status, code)
		return
	}
	_ = json.NewEncoder(w).Encode(page)
}
