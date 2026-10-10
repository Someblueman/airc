package auth

import (
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
	"net/url"
	"strings"
)

func matrixHistoryPath(room, from, to, direction string) string {
	if !matrixwire.RoomID(room) || from != "" && !matrixwire.Cursor(from) || to != "" && !matrixwire.Cursor(to) || from != "" && from == to || direction != "b" && direction != "f" {
		return ""
	}
	q := url.Values{"dir": {direction}, "limit": {"128"}}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	return "/_matrix/client/v3/rooms/" + url.PathEscape(room) + "/messages?" + q.Encode()
}
func matrixHistoryHTTPPath(path string) bool {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.Fragment != "" {
		return false
	}
	parts := strings.Split(u.EscapedPath(), "/")
	if len(parts) != 7 || parts[0] != "" || parts[1] != "_matrix" || parts[2] != "client" || parts[3] != "v3" || parts[4] != "rooms" || parts[6] != "messages" {
		return false
	}
	room, err := url.PathUnescape(parts[5])
	if err != nil {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	expected := matrixHistoryPath(room, q.Get("from"), q.Get("to"), q.Get("dir"))
	return expected != "" && path == expected
}
