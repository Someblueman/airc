package auth

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

// VerifiedMatrixMessage proves an exact event retrieved from the configured
// authenticated homeserver, within its originating-event freshness window.
// The configured homeserver is the timestamp trust boundary; admission is
// separate. The author comes from the event's sender as the homeserver
// reports it. Its zero value grants nothing; parsed data cannot mint a proof.
type VerifiedMatrixMessage struct {
	server                          *MatrixServer
	event                           BridgeEvent
	network, room, author, id, body string
	origin                          int64
}

func (m VerifiedMatrixMessage) Valid() bool               { return m.server != nil && m.event.Valid() }
func (m VerifiedMatrixMessage) Event() BridgeEvent        { return m.event }
func (m VerifiedMatrixMessage) Room() string              { return m.room }
func (m VerifiedMatrixMessage) Author() string            { return m.author }
func (m VerifiedMatrixMessage) Body() string              { return m.body }
func (m VerifiedMatrixMessage) OriginMilliseconds() int64 { return m.origin }
func (m VerifiedMatrixMessage) EventID() string {
	if !m.Valid() {
		return ""
	}
	return m.id
}
func (m VerifiedMatrixMessage) IsRead() bool { return m.Valid() && m.event.IsRead() }
func (m VerifiedMatrixMessage) RequestID() string {
	if !m.Valid() {
		return ""
	}
	return m.event.RequestID()
}
func (m VerifiedMatrixMessage) Occurred() time.Time {
	if !m.Valid() {
		return time.Time{}
	}
	return time.UnixMilli(m.origin).UTC()
}
func (m VerifiedMatrixMessage) ReplayDeadline() time.Time { return m.event.ReplayDeadline() }

func matrixEventPath(room, id string) string {
	if !matrixwire.RoomID(room) || !matrixwire.EventID(id) {
		return ""
	}
	return "/_matrix/client/v3/rooms/" + url.PathEscape(room) + "/event/" + url.PathEscape(id)
}
func matrixHTTPPath(path string) bool {
	if matrixSyncHTTPPath(path) || matrixHistoryHTTPPath(path) {
		return true
	}
	if path == "/_matrix/client/v3/account/whoami" || path == "/_matrix/client/v3/joined_rooms" {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) != 8 || parts[0] != "" || parts[1] != "_matrix" || parts[2] != "client" || parts[3] != "v3" || parts[4] != "rooms" || parts[6] != "event" {
		return false
	}
	room, err := url.PathUnescape(parts[5])
	if err != nil {
		return false
	}
	id, err := url.PathUnescape(parts[7])
	return err == nil && path == matrixEventPath(room, id)
}

// ReadMessage owns account, current joined-room checks and exact event
// retrieval under one three-second budget. A disposition is returned only
// after authenticated event metadata matches the requested room and event.
func (s *MatrixServer) ReadMessage(parent context.Context, room, id string) (VerifiedMatrixMessage, string, error) {
	path := matrixEventPath(room, id)
	if s == nil || parent == nil || path == "" {
		return VerifiedMatrixMessage{}, "", ErrAuthentication
	}
	if !s.hasRoom(room) {
		return VerifiedMatrixMessage{}, "", ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if s.VerifyRooms(ctx) != nil {
		return VerifiedMatrixMessage{}, "", ErrAuthentication
	}
	data, err := s.getMatrixBody(ctx, path, 65536)
	defer clear(data)
	if err != nil {
		return VerifiedMatrixMessage{}, "", ErrAuthentication
	}
	metadata, err := parseMatrixRecoveryMetadata(data, room, id)
	if err != nil {
		return VerifiedMatrixMessage{}, "", ErrAuthentication
	}
	disposition := func(reason string) (VerifiedMatrixMessage, string, error) {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return VerifiedMatrixMessage{}, "", ErrAuthentication
		}
		return VerifiedMatrixMessage{}, reason, nil
	}
	now := time.Now()
	occurred := time.UnixMilli(metadata.origin).UTC()
	until := occurred.Add(s.freshness)
	if occurred.After(now) || !now.Before(until) {
		if occurred.After(now) {
			return disposition("future")
		}
		return disposition("expired")
	}
	m, err := matrixwire.ParseMessage(data, room)
	if err != nil || m.Author != metadata.author || m.ID != id {
		return disposition("unsupported")
	}
	route, routed := s.routes[room]
	if !routed || metadata.author == s.account {
		return disposition("unmapped")
	}
	author, ok := s.authors.Resolve(metadata.author)
	if !ok {
		return disposition("unmapped")
	}
	event := newBridgeEvent(author, route, m.Body, ircDigest("airc-matrix-message-v1", s.network, id), until)
	if !event.Valid() || !event.Refused() && !event.Allowed() && !event.IsRead() {
		return disposition("unsupported")
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return VerifiedMatrixMessage{}, "", ErrAuthentication
	}
	return VerifiedMatrixMessage{server: s, event: event, network: s.network, room: room, author: m.Author, id: id, body: m.Body, origin: m.OriginMilliseconds}, "", nil
}
