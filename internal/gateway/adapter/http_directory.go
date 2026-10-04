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

func httpDirectory(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, token string) {
	limit := 256
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 512 || len(query) > 1 {
		httpFailure(w, 400, core.InvalidRequest)
		return
	}
	if len(query) > 0 {
		values, ok := query["limit"]
		if !ok || len(values) != 1 {
			httpFailure(w, 400, core.InvalidRequest)
			return
		}
		limit, err = strconv.Atoi(values[0])
		if err != nil {
			httpFailure(w, 400, core.InvalidRequest)
			return
		}
	}
	body, _ := json.Marshal(core.Request{Version: 1, RequestID: "directory", Operation: core.Directory, Limit: limit})
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	snapshot, err := d.Directory(ctx, token, body, time.Now())
	if err != nil {
		status, code := httpError(err)
		httpFailure(w, status, code)
		return
	}
	_ = json.NewEncoder(w).Encode(snapshot)
}
