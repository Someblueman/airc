package auth

import (
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// WriteResponse sends one complete plain result (contract JSON) as a private
// chat message to the exact MUC occupant that sent proof. It never fragments.
func (x *XMPPSession) WriteResponse(proof VerifiedXMPPMessage, payload []byte, now time.Time) error {
	if x == nil || !x.usable() || proof.session != x || !proof.Valid() || proof.server != x.server ||
		!x.joined[proof.room] {
		return ErrAuthentication
	}
	bare, _, occupantOK := xmppwire.FullJID(proof.occupant)
	mappedAuthor, _, mapped := xmppwire.FullJID(x.occupants[proof.occupant])
	if !occupantOK || bare != proof.room || !mapped || mappedAuthor != proof.author {
		return ErrAuthentication
	}
	if len(payload) == 0 || len(payload) > MaxResponsePayloadBytes {
		return ErrResponseCapacity
	}
	wire, err := xmppwire.ResponseMessage(proof.occupant, proof.RequestID(), string(payload))
	if err != nil {
		if errors.Is(err, xmppwire.ErrResponseCapacity) {
			return ErrResponseCapacity
		}
		return ErrAuthentication
	}
	deadline := now.Add(5 * time.Second)
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
	if err := x.conn.SetWriteDeadline(time.Time{}); err != nil {
		x.Close()
		return ErrAuthentication
	}
	return nil
}
