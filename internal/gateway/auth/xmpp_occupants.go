package auth

import "github.com/Someblueman/airc/internal/gateway/xmppwire"

const maxXMPPOccupants = 4096

// trackOccupant is serial session state. It retains each occupant's real JID
// as the room reports it; nickname hints and bridge-account echoes cannot
// inherit trust.
// Joining callers may collect initial presence before own confirmation, but
// only a successfully joined room can later be used for an event proof.
func (x *XMPPSession) trackOccupant(e *xmppwire.Element, room string) error {
	if x == nil || x.server == nil || x.occupants == nil {
		return ErrAuthentication
	}
	p, err := xmppwire.Presence(e, room, x.server.account+"/"+x.server.resource)
	if err != nil {
		return ErrAuthentication
	}
	if p.Departed {
		delete(x.occupants, p.Address)
		return nil
	}
	bare, _, ok := xmppwire.FullJID(p.RealJID)
	if !ok {
		return ErrAuthentication
	}
	if bare == x.server.account {
		delete(x.occupants, p.Address)
		return nil
	}
	if _, exists := x.occupants[p.Address]; !exists && len(x.occupants) >= maxXMPPOccupants {
		return ErrAuthentication
	}
	x.occupants[p.Address] = p.RealJID
	return nil
}
