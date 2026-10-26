package adapter

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// discoveryRequest builds the global discovery reads from their GET
// parameters: /v1/directory (?limit, ?nick), /v1/channels (none) and
// /v1/agents (?limit). It reports false for any other path.
func discoveryRequest(r *http.Request) (core.Request, bool, error) {
	var request core.Request
	var allowed map[string]bool
	switch r.URL.Path {
	case "/v1/directory":
		request = core.Request{Version: 1, RequestID: "directory", Operation: core.Directory, Limit: 256}
		allowed = map[string]bool{"limit": true, "nick": true}
	case "/v1/channels":
		request = core.Request{Version: 1, RequestID: "channels", Operation: core.ChannelsRead}
		allowed = map[string]bool{}
	case "/v1/agents":
		request = core.Request{Version: 1, RequestID: "agents", Operation: core.AgentsRead, Limit: core.MaxAgents}
		allowed = map[string]bool{"limit": true}
	default:
		return request, false, nil
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 512 {
		return request, true, store.ErrInvalid
	}
	for key, values := range query {
		if len(values) != 1 || !allowed[key] {
			return request, true, store.ErrInvalid
		}
		switch key {
		case "limit":
			if request.Limit, err = strconv.Atoi(values[0]); err != nil || request.Limit < 1 {
				return request, true, store.ErrInvalid
			}
		case "nick":
			if values[0] == "" {
				return request, true, store.ErrInvalid
			}
			request.Query = values[0]
		}
	}
	if request.Operation == core.AgentsRead && request.Limit > core.MaxAgents {
		return request, true, store.ErrInvalid
	}
	return request, true, request.Normalize()
}
