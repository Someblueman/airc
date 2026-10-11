package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

type matrixNoticeContent struct {
	MsgType   string              `json:"msgtype"`
	Body      string              `json:"body"`
	RelatesTo matrixNoticeRelates `json:"m.relates_to"`
}

type matrixNoticeRelates struct {
	InReplyTo struct {
		EventID string `json:"event_id"`
	} `json:"m.in_reply_to"`
}

// WriteResponse publishes one plain m.notice (the result's contract JSON) in
// the request's room, as a reply to the request event. Matrix has no
// per-member delivery inside a room, so every member sees it, as they see
// the request. Publication is not native AIRC acceptance and this method
// never retries an uncertain PUT.
func (s *MatrixServer) WriteResponse(parent context.Context, proof VerifiedMatrixMessage, payload []byte, now time.Time) error {
	if parent == nil || parent.Err() != nil || s == nil || s.security == nil || len(s.token) == 0 ||
		!proof.Valid() || proof.server != s || proof.network != s.network || !s.hasRoom(proof.room) {
		return ErrAuthentication
	}
	if len(payload) > MaxResponsePayloadBytes {
		return ErrResponseCapacity
	}
	if len(payload) == 0 {
		return ErrAuthentication
	}
	occurred := time.UnixMilli(proof.origin)
	until := proof.ReplayDeadline()
	if proof.origin < 0 || until.IsZero() || !until.Equal(occurred.Add(s.freshness)) ||
		now.Before(occurred) || !now.Before(until) || !time.Now().Before(until) {
		return ErrAuthentication
	}
	deadline := time.Now().Add(5 * time.Second)
	if until.Before(deadline) {
		deadline = until
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	if ctx.Err() != nil || s.VerifyRooms(ctx) != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
		return ErrAuthentication
	}
	content := matrixNoticeContent{MsgType: "m.notice", Body: string(payload)}
	content.RelatesTo.InReplyTo.EventID = proof.id
	body, err := json.Marshal(content)
	if err != nil || len(body) == 0 || len(body) > 65536 {
		clear(body)
		return ErrResponseCapacity
	}
	defer clear(body)
	var transaction [16]byte
	if _, err := rand.Read(transaction[:]); err != nil {
		return ErrAuthentication
	}
	transactionID := hex.EncodeToString(transaction[:])
	clear(transaction[:])
	if matrixSendPath(proof.room, transactionID) == "" || ctx.Err() != nil || !time.Now().Before(deadline) {
		return ErrAuthentication
	}
	if _, err := s.putMatrixNotice(ctx, proof.room, transactionID, body); err != nil {
		return err
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return ErrAuthentication
	}
	return nil
}

func matrixSendPath(room, transactionID string) string {
	if !matrixwire.RoomID(room) || !matrixTransactionID(transactionID) {
		return ""
	}
	return "/_matrix/client/v3/rooms/" + url.PathEscape(room) + "/send/m.room.message/" + transactionID
}

func matrixSendHTTPPath(path string) bool {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.Fragment != "" || u.RawQuery != "" {
		return false
	}
	parts := strings.Split(u.EscapedPath(), "/")
	if len(parts) != 9 || parts[0] != "" || parts[1] != "_matrix" || parts[2] != "client" || parts[3] != "v3" ||
		parts[4] != "rooms" || parts[6] != "send" || parts[7] != "m.room.message" || !matrixTransactionID(parts[8]) {
		return false
	}
	room, err := url.PathUnescape(parts[5])
	return err == nil && path == matrixSendPath(room, parts[8])
}

func matrixTransactionID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
