package auth

import (
	"encoding/json"
	"net/url"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

type matrixSyncFilter struct {
	Room struct {
		Rooms    []string `json:"rooms"`
		Timeline struct {
			Limit int      `json:"limit"`
			Types []string `json:"types"`
		} `json:"timeline"`
	} `json:"room"`
}

func matrixSyncPath(since string, rooms []string) string {
	if since != "" && !matrixwire.Cursor(since) || len(rooms) < 1 || len(rooms) > 128 {
		return ""
	}
	ordered := slices.Clone(rooms)
	slices.Sort(ordered)
	for i, room := range ordered {
		if !matrixwire.RoomID(room) || i > 0 && room == ordered[i-1] {
			return ""
		}
	}
	var filter matrixSyncFilter
	filter.Room.Rooms = ordered
	filter.Room.Timeline.Limit = 128
	filter.Room.Timeline.Types = []string{"m.room.message"}
	raw, err := json.Marshal(filter)
	if err != nil {
		return ""
	}
	query := url.Values{"filter": {string(raw)}, "timeout": {"0"}, "set_presence": {"offline"}}
	if since != "" {
		query.Set("since", since)
	}
	return "/_matrix/client/v3/sync?" + query.Encode()
}

func matrixSyncHTTPPath(path string) bool {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.Path != "/_matrix/client/v3/sync" || u.Fragment != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	var filter matrixSyncFilter
	if jsoninput.Decode([]byte(q.Get("filter")), &filter, 65536) != nil {
		return false
	}
	expected := matrixSyncPath(q.Get("since"), filter.Room.Rooms)
	return expected != "" && path == expected
}
