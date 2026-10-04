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
	"github.com/Someblueman/airc/internal/gateway/store"
)

func httpReadPage(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, token string) {
	body, start, err := readRequest(r)
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	// Leave room within the server's five-second write bound for encoding errors.
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	page, err := d.ReadPage(ctx, token, body, start, time.Now(), nil)
	if err != nil {
		status, code := httpError(err)
		httpFailure(w, status, code)
		return
	}
	_ = json.NewEncoder(w).Encode(page)
}

func httpReadContext(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, token string) {
	body, start, err := readRequest(r)
	if err != nil || start != "" {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := d.ReadContext(ctx, token, body, time.Now())
	if err != nil {
		status, code := httpError(err)
		httpFailure(w, status, code)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func readRequest(r *http.Request) ([]byte, string, error) {
	search := r.URL.Path == "/v1/search"
	contextRead := r.URL.Path == "/v1/context"
	if len(r.URL.RawQuery) > 4096 {
		return nil, "", store.ErrInvalid
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, "", store.ErrInvalid
	}
	for key, values := range query {
		allowed := key == "kind" || key == "target" || key == "limit" || key == "cursor" || !contextRead && key == "start" && !search || !contextRead && (key == "query" || key == "from") && search
		if contextRead {
			allowed = key == "kind" || key == "target" || key == "parent_id" || key == "limit" || key == "max_bytes"
		} else {
			allowed = allowed || key == "parent_id" || key == "thread_mode"
		}
		if len(values) != 1 || !utf8.ValidString(values[0]) || !allowed {
			return nil, "", store.ErrInvalid
		}
	}
	limit := 50
	if raw, ok := query["limit"]; ok {
		limit, err = strconv.Atoi(raw[0])
		if err != nil {
			return nil, "", store.ErrInvalid
		}
	}
	if contextRead {
		maxBytes := 0
		if raw, ok := query["max_bytes"]; ok {
			maxBytes, err = strconv.Atoi(raw[0])
			if err != nil {
				return nil, "", store.ErrInvalid
			}
		}
		request := core.Request{Version: 1, RequestID: "context", Operation: core.ContextRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}, ParentID: query.Get("parent_id"), Limit: limit, MaxBytes: maxBytes}
		body, err := json.Marshal(request)
		if err != nil {
			return nil, "", store.ErrInvalid
		}
		return body, "", nil
	}
	request := core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}, ParentID: query.Get("parent_id"), ThreadMode: query.Get("thread_mode"), Limit: limit, Cursor: query.Get("cursor")}
	if search {
		request.Operation = core.Search
		request.RequestID = "search"
		request.Query = query.Get("query")
		request.From = query.Get("from")
	}
	if r.URL.Path == "/v1/watch" || r.URL.Path == "/v1/events" {
		request.Operation, request.RequestID = core.Watch, "watch"
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, "", store.ErrInvalid
	}
	return body, query.Get("start"), nil
}
