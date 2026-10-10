package auth

import (
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// XMPPResponseRoute binds protected results to this bridge, author, room and
// fixed native target. Occupant nick changes do not change the response scope.
func XMPPResponseRoute(network, author, room string, target core.Target) string {
	targetCopy := target
	if !xmppwire.BareJID(author) || !xmppwire.BareJID(room) || targetCopy.Normalize() != nil {
		return ""
	}
	return ircDigest("airc-xmpp-response-v1", network, author, room, targetCopy.Kind, targetCopy.Name)
}

// WriteResponse sends one complete encrypted result stanza to the exact MUC
// occupant that supplied proof. It never falls back to plaintext or fragments.
func (x *XMPPSession) WriteResponse(proof VerifiedXMPPMessage, payload []byte, now time.Time) error {
	if x == nil || !x.usable() || proof.session != x || !proof.Valid() || proof.server != x.server ||
		!x.joined[proof.room] {
		return ErrAuthentication
	}
	bare, _, occupantOK := xmppwire.FullJID(proof.occupant)
	mappedAuthor, _, mapped := xmppwire.FullJID(x.occupants[proof.occupant])
	if !occupantOK || bare != proof.room || !mapped || mappedAuthor != proof.author || x.server.verifier == nil {
		return ErrAuthentication
	}
	message, freshnessDeadline, err := x.server.verifier.AuthenticateXMPP(proof, now)
	if err != nil || message.Identity.ID() != proof.identity.ID() || message.Request.Target == nil || *message.Request.Target != proof.target {
		return ErrAuthentication
	}
	grant, ok := x.server.grants[[2]string{proof.author, proof.room}]
	if !ok || grant.identity.ID() != proof.identity.ID() || grant.responseRecipientPublicKey == nil ||
		grant.responseRecipientKeyID == "" || x.server.responseSealer == nil {
		return ErrAuthentication
	}
	if len(payload) == 0 || len(payload) > MaxResponsePayloadBytes {
		return ErrResponseCapacity
	}
	route := XMPPResponseRoute(proof.network, proof.author, proof.room, proof.target)
	if route == "" {
		return ErrAuthentication
	}
	token, err := x.server.responseSealer.Seal(ResponseScope{
		Realm: x.server.realm, Principal: proof.identity.ID(), Binding: proof.identity.Binding(), Route: route,
	}, grant.responseRecipientKeyID, grant.responseRecipientPublicKey, payload, now)
	if err != nil {
		return err
	}
	wire, err := xmppwire.ResponseMessage(proof.occupant, message.Request.RequestID, token)
	if err != nil {
		if errors.Is(err, xmppwire.ErrResponseCapacity) {
			return ErrResponseCapacity
		}
		return ErrAuthentication
	}
	deadline := now.Add(5 * time.Second)
	if freshnessDeadline.Before(deadline) {
		deadline = freshnessDeadline
	}
	if !time.Now().Before(deadline) || !x.usable() || x.parent.Err() != nil {
		return ErrAuthentication
	}
	if err := x.conn.SetWriteDeadline(deadline); err != nil {
		x.Close()
		return ErrAuthentication
	}
	if err := x.write(wire); err != nil {
		x.Close()
		return ErrAuthentication
	}
	if !time.Now().Before(deadline) {
		x.Close()
		return ErrAuthentication
	}
	if err := x.conn.SetWriteDeadline(time.Time{}); err != nil {
		x.Close()
		return ErrAuthentication
	}
	return nil
}
