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
	request := core.Request{Version: 1, RequestID: "directory", Operation: core.Directory, Limit: 256}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 512 {
		httpFailure(w, 400, core.InvalidRequest)
		return
	}
	for key, values := range query {
		if len(values) != 1 {
			httpFailure(w, 400, core.InvalidRequest)
			return
		}
		switch key {
		case "limit":
			request.Limit, err = strconv.Atoi(values[0])
			if err != nil {
				httpFailure(w, 400, core.InvalidRequest)
				return
			}
		case "nick":
			if values[0] == "" {
				httpFailure(w, 400, core.InvalidRequest)
				return
			}
			request.Query = values[0]
		default:
			httpFailure(w, 400, core.InvalidRequest)
			return
		}
	}
	body, _ := json.Marshal(request)
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
