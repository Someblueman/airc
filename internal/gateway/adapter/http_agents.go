package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func httpAgents(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, token string) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 512 {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	limit := core.MaxAgents
	if values, ok := query["limit"]; ok {
		if len(values) != 1 {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > core.MaxAgents {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
	}
	for key := range query {
		if key != "limit" {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
	}
	body, err := json.Marshal(core.Request{Version: 1, RequestID: "agents", Operation: core.AgentsRead, Limit: limit})
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := d.ReadAgents(ctx, token, body, time.Now())
	if err != nil {
		status, code := httpError(err)
		httpFailure(w, status, code)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxAgentsBytes {
		httpFailure(w, http.StatusTooManyRequests, core.CapacityExceeded)
		return
	}
	_, _ = w.Write(append(encoded, '\n'))
}
