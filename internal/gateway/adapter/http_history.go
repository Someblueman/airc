package adapter

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func readRequest(r *http.Request) ([]byte, string, error) {
	search := r.URL.Path == "/v1/search"
	contextRead := r.URL.Path == "/v1/context"
	pinsRead := r.URL.Path == "/v1/pins"
	roomRead := r.URL.Path == "/v1/room"
	waitingRead := r.URL.Path == "/v1/waiting"
	pollRead := r.URL.Path == "/v1/poll"
	topicRead := r.URL.Path == "/v1/topic"
	membersRead := r.URL.Path == "/v1/members"
	if len(r.URL.RawQuery) > 4096 {
		return nil, "", store.ErrInvalid
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, "", store.ErrInvalid
	}
	for key, values := range query {
		allowed := key == "kind" || key == "target" || key == "limit" || key == "cursor" || !contextRead && key == "start" && !search || !contextRead && (key == "query" || key == "from") && search
		if pinsRead || roomRead {
			allowed = key == "kind" || key == "target"
		} else if waitingRead {
			allowed = key == "kind" || key == "target" || key == "parent_id"
		} else if pollRead {
			allowed = key == "kind" || key == "target" || key == "poll_id"
		} else if topicRead || membersRead {
			allowed = key == "kind" || key == "target"
		} else if contextRead {
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
	if pinsRead {
		request := core.Request{Version: 1, RequestID: "pins", Operation: core.PinsRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}}
		body, err := json.Marshal(request)
		if err != nil {
			return nil, "", store.ErrInvalid
		}
		return body, "", nil
	}
	if roomRead {
		request := core.Request{Version: 1, RequestID: "room", Operation: core.RoomRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}}
		body, err := json.Marshal(request)
		if err != nil {
			return nil, "", store.ErrInvalid
		}
		return body, "", nil
	}
	if waitingRead {
		request := core.Request{Version: 1, RequestID: "waiting", Operation: core.WaitingRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}, ParentID: query.Get("parent_id")}
		body, err := json.Marshal(request)
		if err != nil {
			return nil, "", store.ErrInvalid
		}
		return body, "", nil
	}
	if pollRead {
		request := core.Request{Version: 1, RequestID: "poll", Operation: core.PollResults, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}, PollID: query.Get("poll_id")}
		body, err := json.Marshal(request)
		if err != nil {
			return nil, "", store.ErrInvalid
		}
		return body, "", nil
	}
	if topicRead {
		request := core.Request{Version: 1, RequestID: "topic", Operation: core.TopicRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}}
		body, err := json.Marshal(request)
		if err != nil {
			return nil, "", store.ErrInvalid
		}
		return body, "", nil
	}
	if membersRead {
		request := core.Request{Version: 1, RequestID: "members", Operation: core.MembersRead, Target: &core.Target{Kind: query.Get("kind"), Name: query.Get("target")}}
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
	} else if r.URL.Path == "/v1/live" || r.URL.Path == "/v1/live/events" {
		request.Operation, request.RequestID = core.EventsWatch, "events-watch"
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, "", store.ErrInvalid
	}
	return body, query.Get("start"), nil
}
