package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func httpChannels(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, token string) {
	if r.URL.RawQuery != "" {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	request := core.Request{Version: 1, RequestID: "channels", Operation: core.ChannelsRead}
	body, err := json.Marshal(request)
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := d.ReadChannels(ctx, token, body, time.Now())
	if err != nil {
		status, code := httpError(err)
		httpFailure(w, status, code)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxChannelsBytes {
		httpFailure(w, http.StatusTooManyRequests, core.CapacityExceeded)
		return
	}
	_, _ = w.Write(append(encoded, '\n'))
}
