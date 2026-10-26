package adapter

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// readPaths are the GET routes that read pages or snapshots and take a query.
var readPaths = map[string]bool{
	"/v1/history": true, "/v1/search": true, "/v1/watch": true, "/v1/events": true,
	"/v1/live": true, "/v1/live/events": true, "/v1/directory": true, "/v1/channels": true,
	"/v1/agents": true, "/v1/context": true, "/v1/pins": true, "/v1/room": true,
	"/v1/waiting": true, "/v1/poll": true, "/v1/topic": true, "/v1/members": true,
}

// targetReads are the snapshot reads of one room target, with at most one
// identifier parameter.
var targetReads = map[string]struct {
	operation core.Operation
	id        string
	param     string // "", "parent_id" or "poll_id"
}{
	"/v1/pins":    {core.PinsRead, "pins", ""},
	"/v1/room":    {core.RoomRead, "room", ""},
	"/v1/topic":   {core.TopicRead, "topic", ""},
	"/v1/members": {core.MembersRead, "members", ""},
	"/v1/waiting": {core.WaitingRead, "waiting", "parent_id"},
	"/v1/poll":    {core.PollResults, "poll", "poll_id"},
}

func readRequest(r *http.Request) ([]byte, string, error) {
	search := r.URL.Path == "/v1/search"
	contextRead := r.URL.Path == "/v1/context"
	target, targetRead := targetReads[r.URL.Path]
	if len(r.URL.RawQuery) > 4096 {
		return nil, "", store.ErrInvalid
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, "", store.ErrInvalid
	}
	for key, values := range query {
		var allowed bool
		switch {
		case targetRead:
			allowed = key == "kind" || key == "target" || target.param != "" && key == target.param
		case contextRead:
			allowed = key == "kind" || key == "target" || key == "parent_id" || key == "limit" || key == "max_bytes"
		default:
			allowed = key == "kind" || key == "target" || key == "limit" || key == "cursor" || key == "start" && !search || (key == "query" || key == "from") && search || key == "parent_id" || key == "thread_mode"
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
	request := core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}}
	start := ""
	switch {
	case targetRead:
		request.RequestID, request.Operation = target.id, target.operation
		switch target.param {
		case "parent_id":
			request.ParentID = query.Get("parent_id")
		case "poll_id":
			request.PollID = query.Get("poll_id")
		}
	case contextRead:
		maxBytes := 0
		if raw, ok := query["max_bytes"]; ok {
			maxBytes, err = strconv.Atoi(raw[0])
			if err != nil {
				return nil, "", store.ErrInvalid
			}
		}
		request.RequestID, request.Operation = "context", core.ContextRead
		request.ParentID, request.Limit, request.MaxBytes = query.Get("parent_id"), limit, maxBytes
	default:
		request.ParentID, request.ThreadMode, request.Limit, request.Cursor = query.Get("parent_id"), query.Get("thread_mode"), limit, query.Get("cursor")
		start = query.Get("start")
		switch r.URL.Path {
		case "/v1/search":
			request.Operation, request.RequestID = core.Search, "search"
			request.Query, request.From = query.Get("query"), query.Get("from")
		case "/v1/watch", "/v1/events":
			request.Operation, request.RequestID = core.Watch, "watch"
		case "/v1/live", "/v1/live/events":
			request.Operation, request.RequestID = core.EventsWatch, "events-watch"
		}
	}
	// Encoded and decoded again by the caller: core.Decode applies the strict
	// JSON checks and the request size limit to query-built requests too.
	body, err := json.Marshal(request)
	if err != nil {
		return nil, "", store.ErrInvalid
	}
	return body, start, nil
}
