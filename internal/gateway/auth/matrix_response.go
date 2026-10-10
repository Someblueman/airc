package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

type matrixNoticeContent struct {
	MsgType string `json:"msgtype"`
	Body    string `json:"body"`
}

// WriteResponse publishes one fixed-room encrypted notice for a verified AIRC
// Send or read request. The notice contains ciphertext only; publication is not native
// AIRC acceptance and this method never retries an uncertain PUT.
func (s *MatrixServer) WriteResponse(parent context.Context, proof VerifiedMatrixMessage, payload []byte, now time.Time) error {
	if parent == nil || parent.Err() != nil || s == nil || s.security == nil || len(s.token) == 0 || s.responseSealer == nil ||
		!proof.Valid() || proof.server != s || proof.network != s.network ||
		s.verifier == nil || !s.verifier.OwnsIdentity(proof.identity) {
		return ErrAuthentication
	}
	if len(payload) > MaxResponsePayloadBytes {
		return ErrResponseCapacity
	}
	if len(payload) == 0 {
		return ErrAuthentication
	}
	grant, ok := s.grants[[2]string{proof.author, proof.room}]
	if !ok || grant.identity.principal != proof.identity.principal || grant.identity.ID() != proof.identity.ID() ||
		grant.identity.Binding() != proof.identity.Binding() || grant.target != proof.target ||
		grant.responseRecipientKeyID == "" || grant.responseRecipientPublicKey == nil {
		return ErrAuthentication
	}
	operation := core.Send
	if proof.IsRead() {
		request, err := proof.ReadRequest()
		if err != nil {
			return ErrAuthentication
		}
		operation = request.Operation
	}
	if !slices.Contains(grant.operations, operation) || !slices.Contains(proof.operations, operation) {
		return ErrAuthentication
	}
	occurred := time.UnixMilli(proof.origin)
	if proof.origin < 0 || proof.until.IsZero() || !proof.until.Equal(occurred.Add(s.freshness)) ||
		now.Before(occurred) || !now.Before(proof.until) || !time.Now().Before(proof.until) {
		return ErrAuthentication
	}
	route := MatrixResponseRoute(proof.network, proof.author, proof.room, proof.target)
	if route == "" {
		return ErrAuthentication
	}
	deadline := time.Now().Add(5 * time.Second)
	if proof.until.Before(deadline) {
		deadline = proof.until
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	if ctx.Err() != nil || s.VerifyRooms(ctx) != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
		return ErrAuthentication
	}
	token, err := s.responseSealer.Seal(ResponseScope{
		Realm: s.realm, Principal: proof.identity.ID(), Binding: proof.identity.Binding(), Route: route,
	}, grant.responseRecipientKeyID, grant.responseRecipientPublicKey, payload, now)
	if err != nil {
		return err
	}
	body, err := json.Marshal(matrixNoticeContent{MsgType: "m.notice", Body: token})
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
