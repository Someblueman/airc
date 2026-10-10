package auth

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

// VerifiedMatrixMessage proves an exact event retrieved from the configured
// authenticated homeserver, within its originating-event freshness window.
// The configured homeserver is the timestamp trust boundary; admission is separate.
// Its zero value grants nothing; parsed data cannot mint a proof.
type VerifiedMatrixMessage struct {
	server                          *MatrixServer
	identity                        Identity
	target                          core.Target
	operations                      []core.Operation
	network, room, author, id, body string
	origin                          int64
	until                           time.Time
}

func (m VerifiedMatrixMessage) Valid() bool                  { return m.server != nil && m.identity.ID() != "" }
func (m VerifiedMatrixMessage) Identity() Identity           { return m.identity }
func (m VerifiedMatrixMessage) Target() core.Target          { return m.target }
func (m VerifiedMatrixMessage) Operations() []core.Operation { return slices.Clone(m.operations) }
func (m VerifiedMatrixMessage) Room() string                 { return m.room }
func (m VerifiedMatrixMessage) Author() string               { return m.author }
func (m VerifiedMatrixMessage) Body() string                 { return m.body }
func (m VerifiedMatrixMessage) OriginMilliseconds() int64    { return m.origin }
func (m VerifiedMatrixMessage) RequestID() string {
	if !m.Valid() {
		return ""
	}
	return ircDigest("airc-matrix-message-v1", m.network, m.id)
}

func (m VerifiedMatrixMessage) Occurred() time.Time {
	if !m.Valid() {
		return time.Time{}
	}
	return time.UnixMilli(m.origin).UTC()
}
func (m VerifiedMatrixMessage) ReplayDeadline() time.Time { return m.until }
func (m VerifiedMatrixMessage) Fingerprint() string {
	if !m.Valid() {
		return ""
	}
	return ircDigest("airc-matrix-content-v1", m.network, m.id, m.author, m.room,
		strconv.FormatInt(m.origin, 10), m.body, m.identity.ID(), m.identity.Binding(),
		string(m.target.Kind), m.target.Name)
}

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
	if err != nil {
		return disposition("unsupported")
	}
	grant, ok := s.grants[[2]string{metadata.author, room}]
	if !ok || metadata.author == s.account {
		return disposition("unmapped")
	}
	if s.verifier == nil || !s.verifier.OwnsIdentity(grant.identity) || !slices.Contains(grant.operations, core.Send) {
		return disposition("unsupported")
	}
	target := grant.target
	request := core.Request{Version: 1, RequestID: ircDigest("airc-matrix-message-v1", s.network, id), Operation: core.Send, Target: &target, Message: m.Body}
	if request.Normalize() != nil || request.Message != m.Body || request.Target == nil || *request.Target != grant.target {
		return disposition("unsupported")
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return VerifiedMatrixMessage{}, "", ErrAuthentication
	}
	return VerifiedMatrixMessage{server: s, identity: grant.identity, target: grant.target, operations: slices.Clone(grant.operations), network: s.network, room: room, author: m.Author, id: id, body: m.Body, origin: m.OriginMilliseconds, until: until}, "", nil
}
